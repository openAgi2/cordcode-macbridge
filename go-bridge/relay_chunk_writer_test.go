package gobridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRelayChunkWriterReassemblesAndAuthenticatesEveryChunk(t *testing.T) {
	key := make([]byte, 32)
	out := make(chan RelayEnvelope, 16)
	rc := NewRelayDeviceConn("device", "bridge", "route", 9, nil, append([]byte(nil), key...), nil, func(raw json.RawMessage) error {
		var env RelayEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		out <- env
		return nil
	})
	w := newRelayOutboundWriter()
	defer w.close()
	rc.setOutboundWriter(w)
	rc.mu.Lock()
	rc.outboundChunks = true
	rc.mu.Unlock()
	rc.registerRequestClass("history", "get_session_messages", "")
	rc.advanceSessionBulkGeneration("session", "history")
	rc.SendResult("history", map[string]any{"messages": []any{map[string]any{"text": string(make([]byte, 100000))}}}, nil)

	var combined []byte
	var groupID string
	var count uint32
	for {
		select {
		case env := <-out:
			if env.Chunk == nil {
				t.Fatal("bulk result was not chunked")
			}
			if groupID == "" {
				groupID, count = env.Chunk.GroupID, env.Chunk.Count
			}
			if env.Chunk.GroupID != groupID || env.Chunk.Count != count || env.Chunk.Index != uint32(len(combined)/relayChunkTargetBytes) {
				t.Fatalf("chunk metadata drift: %+v", env.Chunk)
			}
			if env.Counter != uint64(env.Chunk.Index)+1 {
				t.Fatalf("counter=%d index=%d", env.Counter, env.Chunk.Index)
			}
			aad, err := env.EncodeAAD()
			if err != nil {
				t.Fatal(err)
			}
			plain, err := OpenEnvelope(key, env.Counter, aad, env.Ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			combined = append(combined, plain...)
			if env.Chunk.Index+1 == count {
				var response struct {
					Type      string `json:"type"`
					RequestID string `json:"requestId"`
					OK        bool   `json:"ok"`
				}
				if err := json.Unmarshal(combined, &response); err != nil {
					t.Fatal(err)
				}
				if response.Type != "result" || response.RequestID != "history" || !response.OK {
					t.Fatalf("response=%+v", response)
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for chunk group")
		}
	}
}

func TestRelayChunkWriterStressCounterAndNonceOrder(t *testing.T) {
	key := make([]byte, 32)
	const bulkJobs = 1000
	const controlJobs = 50
	const chunksPerBulk = 2
	expected := bulkJobs*chunksPerBulk + controlJobs
	var mu sync.Mutex
	counters := make(map[uint64]struct{}, expected)
	delivered := make(chan struct{})
	rc := NewRelayDeviceConn("device", "bridge", "route", 1, nil, append([]byte(nil), key...), nil, func(raw json.RawMessage) error {
		var env RelayEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		mu.Lock()
		if _, duplicate := counters[env.Counter]; duplicate {
			mu.Unlock()
			return fmt.Errorf("duplicate counter %d", env.Counter)
		}
		counters[env.Counter] = struct{}{}
		if len(counters) == expected {
			close(delivered)
		}
		mu.Unlock()
		return nil
	})
	w := newRelayOutboundWriter()
	defer w.close()
	rc.setOutboundWriter(w)
	payload := make([]byte, relayChunkTargetBytes+1)
	for i := 0; i < bulkJobs; i++ {
		handle := newOutboundBulkHandle(generateRelayID("stress_"))
		job := &relayOutboundJob{conn: rc, payload: append([]byte(nil), payload...), cursor: &relayChunkCursor{
			groupID: handle.GroupID(), count: chunksPerBulk, chunkBytes: relayChunkTargetBytes,
			handle: handle, channelGeneration: 1, expiresAt: time.Now().Add(time.Minute),
		}}
		if err := w.admitBulk(job); err != nil {
			t.Fatal(err)
		}
	}
	controlDone := make(chan error, 1)
	go func() {
		for i := 0; i < controlJobs; i++ {
			if err := w.enqueue(&relayOutboundJob{conn: rc, payload: []byte(`{"type":"ping"}`), class: relayOutboundControl}); err != nil {
				controlDone <- err
				return
			}
		}
		controlDone <- nil
	}()
	select {
	case <-delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("stress delivery timed out")
	}
	if err := <-controlDone; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(counters) != expected {
		t.Fatalf("envelopes=%d want=%d", len(counters), expected)
	}
	for counter := uint64(1); counter <= uint64(expected); counter++ {
		if _, ok := counters[counter]; !ok {
			t.Fatalf("counter gap at %d", counter)
		}
	}
	if relayResultBypassWriter.Load() != 0 {
		t.Fatalf("relay_result_bypass_writer=%d", relayResultBypassWriter.Load())
	}
}

func TestRelayChunkWriterPreemptsAndFinishesStartedGroupAfterSupersede(t *testing.T) {
	key := make([]byte, 32)
	var mu sync.Mutex
	requestOrder := []string{}
	firstChunk := make(chan struct{})
	releaseFirst := make(chan struct{})
	groupDone := make(chan struct{})
	var groupDoneOnce sync.Once
	chunkWrites := 0
	expectedChunkWrites := 0
	var rc *RelayDeviceConn
	rc = NewRelayDeviceConn("device", "bridge", "route", 1, nil, append([]byte(nil), key...), nil, func(raw json.RawMessage) error {
		var env RelayEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		aad, err := env.EncodeAAD()
		if err != nil {
			return err
		}
		plain, err := OpenEnvelope(key, env.Counter, aad, env.Ciphertext)
		if err != nil {
			return err
		}
		label := "chunk"
		if env.Chunk == nil {
			var response struct {
				RequestID string `json:"requestId"`
			}
			if err := json.Unmarshal(plain, &response); err != nil {
				return err
			}
			label = response.RequestID
		}
		mu.Lock()
		if label == "chunk" {
			chunkWrites++
			if expectedChunkWrites == 0 {
				expectedChunkWrites = int(env.Chunk.Count)
			}
			if chunkWrites == expectedChunkWrites {
				groupDoneOnce.Do(func() { close(groupDone) })
			}
		}
		currentChunkWrites := chunkWrites
		requestOrder = append(requestOrder, label)
		n := len(requestOrder)
		mu.Unlock()
		if n == 1 {
			close(firstChunk)
			<-releaseFirst
		}
		if currentChunkWrites == 3 {
			rc.advanceSessionBulkGeneration("session", "new-history")
		}
		return nil
	})
	w := newRelayOutboundWriter()
	defer w.close()
	rc.setOutboundWriter(w)
	rc.mu.Lock()
	rc.outboundChunks = true
	rc.mu.Unlock()
	rc.registerRequestClass("history", "get_session_messages", "")
	rc.advanceSessionBulkGeneration("session", "history")
	rc.SendResult("history", map[string]any{"messages": string(make([]byte, 100000))}, nil)
	<-firstChunk
	done := make(chan struct{})
	rc.registerRequestClass("models", "list_models", "")
	go func() { rc.SendResult("models", map[string]any{"models": []any{}}, nil); close(done) }()
	deadline := time.Now().Add(time.Second)
	for {
		w.mu.Lock()
		queued := len(w.queues[relayOutboundMetadata])
		w.mu.Unlock()
		if queued == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metadata job was not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseFirst)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("metadata result did not complete")
	}
	select {
	case <-groupDone:
	case <-time.After(2 * time.Second):
		t.Fatal("started chunk group did not finish")
	}
	mu.Lock()
	got := append([]string(nil), requestOrder...)
	writes := chunkWrites
	wantWrites := expectedChunkWrites
	mu.Unlock()
	if len(got) < 2 || got[0] != "chunk" || got[1] != "models" {
		t.Fatalf("wire order=%v", got)
	}
	if writes != wantWrites {
		t.Fatalf("started chunk group did not finish after supersede: writes=%d want=%d order=%v", writes, wantWrites, got)
	}
	rc.mu.Lock()
	leaked := rc.activeBulkHandles["session"]
	rc.mu.Unlock()
	if leaked != nil {
		t.Fatal("superseded bulk handle leaked")
	}
}

func TestRelayChunkWriterSerializesConcurrentGroupsPerDevice(t *testing.T) {
	key := make([]byte, 32)
	var mu sync.Mutex
	groupOrder := []string{}
	groupCounts := map[string]int{}
	expectedCounts := map[string]int{}
	firstChunk := make(chan struct{})
	releaseFirst := make(chan struct{})
	delivered := make(chan struct{})
	var firstOnce sync.Once
	var deliveredOnce sync.Once

	rc := NewRelayDeviceConn("device", "bridge", "route", 1, nil, key, nil, func(raw json.RawMessage) error {
		var env RelayEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		if env.Chunk == nil {
			return nil
		}
		mu.Lock()
		groupOrder = append(groupOrder, env.Chunk.GroupID)
		groupCounts[env.Chunk.GroupID]++
		expectedCounts[env.Chunk.GroupID] = int(env.Chunk.Count)
		completeGroups := 0
		for groupID, count := range groupCounts {
			if count == expectedCounts[groupID] {
				completeGroups++
			}
		}
		mu.Unlock()
		firstOnce.Do(func() {
			close(firstChunk)
			<-releaseFirst
		})
		if completeGroups == 2 {
			deliveredOnce.Do(func() { close(delivered) })
		}
		return nil
	})
	w := newRelayOutboundWriter()
	defer w.close()
	rc.setOutboundWriter(w)
	rc.mu.Lock()
	rc.outboundChunks = true
	rc.mu.Unlock()

	rc.registerRequestClass("history-a", "get_session_messages", "")
	rc.advanceSessionBulkGeneration("session-a", "history-a")
	rc.SendResult("history-a", map[string]any{"messages": string(make([]byte, 200000))}, nil)
	select {
	case <-firstChunk:
	case <-time.After(time.Second):
		t.Fatal("first chunk did not start")
	}
	rc.registerRequestClass("history-b", "get_session_messages", "")
	rc.advanceSessionBulkGeneration("session-b", "history-b")
	rc.SendResult("history-b", map[string]any{"messages": string(make([]byte, 180000))}, nil)
	close(releaseFirst)
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent chunk groups did not complete")
	}

	mu.Lock()
	got := append([]string(nil), groupOrder...)
	groups := len(groupCounts)
	mu.Unlock()
	if groups != 2 {
		t.Fatalf("groups=%d want=2 order=%v", groups, got)
	}
	firstGroup := got[0]
	seenSwitch := false
	for _, groupID := range got[1:] {
		if groupID != firstGroup {
			seenSwitch = true
			continue
		}
		if seenSwitch {
			t.Fatalf("same-device chunk groups interleaved: order=%v", got)
		}
	}
}

func TestRelayChunkWriterFailsTransportWhenStartedGroupExpires(t *testing.T) {
	rc := NewRelayDeviceConn("device", "bridge", "route", 1, nil, make([]byte, 32), nil, nil)
	job := &relayOutboundJob{
		conn:    rc,
		payload: make([]byte, relayChunkTargetBytes*2),
		cursor: &relayChunkCursor{
			groupID:           "started-group",
			nextIndex:         1,
			count:             2,
			chunkBytes:        relayChunkTargetBytes,
			handle:            newOutboundBulkHandle("started-group"),
			channelGeneration: 1,
			expiresAt:         time.Now().Add(-time.Second),
		},
	}
	w := newRelayOutboundWriter()
	defer w.close()
	err, complete := w.writeSelected(job)
	if !complete || err == nil || !strings.Contains(err.Error(), "expired after transmission began") {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
}
