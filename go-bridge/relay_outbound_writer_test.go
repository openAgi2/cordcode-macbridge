package gobridge

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestRelayOutboundWriterPrioritizesMetadataAfterCurrentFrame(t *testing.T) {
	writer := newRelayOutboundWriter()
	defer writer.close()

	var mu sync.Mutex
	order := make([]string, 0, 3)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	newConn := func(device string, block bool) *RelayDeviceConn {
		return NewRelayDeviceConn(device, "bridge", "route", 1, nil, make([]byte, 32), nil, func(raw json.RawMessage) error {
			mu.Lock()
			order = append(order, device)
			mu.Unlock()
			if block {
				close(firstStarted)
				<-releaseFirst
			}
			return nil
		})
	}
	bulkA := newConn("bulk-a", true)
	bulkB := newConn("bulk-b", false)
	metadata := newConn("metadata", false)

	done := make(chan error, 3)
	go func() {
		done <- writer.enqueue(&relayOutboundJob{conn: bulkA, payload: []byte(`{"type":"result"}`), class: relayOutboundBulk})
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first bulk frame did not start")
	}
	go func() {
		done <- writer.enqueue(&relayOutboundJob{conn: bulkB, payload: []byte(`{"type":"result"}`), class: relayOutboundBulk})
	}()
	go func() {
		done <- writer.enqueue(&relayOutboundJob{conn: metadata, payload: []byte(`{"type":"result"}`), class: relayOutboundMetadata})
	}()
	time.Sleep(10 * time.Millisecond)
	close(releaseFirst)
	for range 3 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if want := []string{"bulk-a", "metadata", "bulk-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("wire order = %v, want %v", got, want)
	}
}

func TestRelayOutboundWriterRoundRobinsBulkDevices(t *testing.T) {
	writer := newRelayOutboundWriter()
	defer writer.close()
	connA := NewRelayDeviceConn("device-a", "bridge", "route", 1, nil, make([]byte, 32), nil, nil)
	connB := NewRelayDeviceConn("device-b", "bridge", "route", 1, nil, make([]byte, 32), nil, nil)
	jobs := []*relayOutboundJob{
		{conn: connA, payload: []byte("a1"), class: relayOutboundBulk},
		{conn: connA, payload: []byte("a2"), class: relayOutboundBulk},
		{conn: connB, payload: []byte("b1"), class: relayOutboundBulk},
	}
	writer.mu.Lock()
	writer.queues[relayOutboundBulk] = append(writer.queues[relayOutboundBulk], jobs...)
	writer.queueFrames, writer.bulkFrames = len(jobs), len(jobs)
	for _, job := range jobs {
		writer.queueBytes += len(job.payload)
		writer.bulkBytes += len(job.payload)
	}
	writer.mu.Unlock()

	got := []string{writer.pop().conn.deviceID, writer.pop().conn.deviceID, writer.pop().conn.deviceID}
	if want := []string{"device-a", "device-b", "device-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("device order=%v want=%v", got, want)
	}
}

func TestRelayOutboundWriterControlBurstLetsMetadataRun(t *testing.T) {
	writer := newRelayOutboundWriter()
	defer writer.close()
	control := NewRelayDeviceConn("control", "bridge", "route", 1, nil, make([]byte, 32), nil, nil)
	metadata := NewRelayDeviceConn("metadata", "bridge", "route", 1, nil, make([]byte, 32), nil, nil)
	writer.mu.Lock()
	for i := 0; i < relayControlBurstCap+2; i++ {
		job := &relayOutboundJob{conn: control, payload: []byte("c"), class: relayOutboundControl}
		writer.queues[relayOutboundControl] = append(writer.queues[relayOutboundControl], job)
		writer.queueFrames++
		writer.queueBytes++
	}
	writer.queues[relayOutboundMetadata] = append(writer.queues[relayOutboundMetadata], &relayOutboundJob{conn: metadata, payload: []byte("m"), class: relayOutboundMetadata})
	writer.queueFrames++
	writer.queueBytes++
	writer.mu.Unlock()

	for i := 0; i < relayControlBurstCap; i++ {
		if job := writer.pop(); job.class != relayOutboundControl {
			t.Fatalf("job %d class=%v want control", i, job.class)
		}
	}
	if job := writer.pop(); job.class != relayOutboundMetadata {
		t.Fatalf("job after control burst class=%v want metadata", job.class)
	}
}

func TestRelayBulkOverflowReturnsExplicitResult(t *testing.T) {
	key := make([]byte, 32)
	out := make(chan RelayEnvelope, 1)
	rc := NewRelayDeviceConn("device", "bridge", "route", 1, nil, key, nil, func(raw json.RawMessage) error {
		var envelope RelayEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return err
		}
		out <- envelope
		return nil
	})
	writer := newRelayOutboundWriter()
	defer writer.close()
	rc.setOutboundWriter(writer)
	rc.mu.Lock()
	rc.outboundChunks = true
	rc.mu.Unlock()
	rc.registerRequestClass("history", "get_session_messages")
	rc.advanceSessionBulkGeneration("session", "history")
	writer.mu.Lock()
	writer.bulkFrames = relayOutboundBulkFrames
	writer.mu.Unlock()
	rc.SendResult("history", map[string]any{"messages": string(make([]byte, relayChunkTargetBytes+1))}, nil)

	select {
	case envelope := <-out:
		if envelope.Chunk != nil {
			t.Fatal("overload response must not be chunked")
		}
		aad, err := envelope.EncodeAAD()
		if err != nil {
			t.Fatal(err)
		}
		plain, err := OpenEnvelope(key, envelope.Counter, aad, envelope.Ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			OK    bool       `json:"ok"`
			Error *WireError `json:"error"`
		}
		if err := json.Unmarshal(plain, &response); err != nil {
			t.Fatal(err)
		}
		if response.OK || response.Error == nil || response.Error.Code != "relay.overloaded" {
			t.Fatalf("response=%+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit overload result was not delivered")
	}
}

func TestRelayResultClassRegistryIsConsumedOnce(t *testing.T) {
	rc := NewRelayDeviceConn("device", "bridge", "route", 1, nil, make([]byte, 32), nil, func(json.RawMessage) error { return nil })
	rc.registerRequestClass("req", "list_models")
	rc.mu.Lock()
	class, exists := rc.requestClasses["req"]
	rc.mu.Unlock()
	if !exists || class != relayOutboundMetadata {
		t.Fatalf("class = %v exists=%v", class, exists)
	}
	rc.SendResult("req", map[string]any{"models": []any{}}, nil)
	rc.mu.Lock()
	_, exists = rc.requestClasses["req"]
	rc.mu.Unlock()
	if exists {
		t.Fatal("request class registry entry leaked after result")
	}
}

func TestRelayUnifiedWriterMatchesLegacyGzipSemantics(t *testing.T) {
	key := make([]byte, 32)
	payload := map[string]any{"type": "result", "data": string(make([]byte, relayGzipThreshold+1024))}
	capture := func() (chan RelayEnvelope, func(json.RawMessage) error) {
		out := make(chan RelayEnvelope, 1)
		return out, func(raw json.RawMessage) error {
			var env RelayEnvelope
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			out <- env
			return nil
		}
	}
	legacyOut, legacySend := capture()
	legacy := NewRelayDeviceConn("legacy", "bridge", "route", 1, nil, append([]byte(nil), key...), nil, legacySend)
	legacy.enableOutboundGzip()
	legacy.SendJSON(payload)

	writerOut, writerSend := capture()
	modern := NewRelayDeviceConn("modern", "bridge", "route", 1, nil, append([]byte(nil), key...), nil, writerSend)
	modern.enableOutboundGzip()
	writer := newRelayOutboundWriter()
	defer writer.close()
	modern.setOutboundWriter(writer)
	modern.SendJSON(payload)

	for _, env := range []RelayEnvelope{<-legacyOut, <-writerOut} {
		if env.Counter != 1 || env.ContentEncoding != "gzip" || env.Chunk != nil {
			t.Fatalf("unexpected envelope semantics: counter=%d encoding=%q chunk=%v", env.Counter, env.ContentEncoding, env.Chunk)
		}
		aad, err := env.EncodeAAD()
		if err != nil {
			t.Fatal(err)
		}
		compressed, err := OpenEnvelope(key, env.Counter, aad, env.Ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			t.Fatal(err)
		}
		plain, err := io.ReadAll(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if err := decoded.Close(); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(plain, &got); err != nil {
			t.Fatal(err)
		}
		if got["type"] != "result" {
			t.Fatalf("decoded payload = %#v", got)
		}
	}
}
