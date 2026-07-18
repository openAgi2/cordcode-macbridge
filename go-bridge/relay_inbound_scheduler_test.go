package gobridge

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func inboundRequest(id, method, session string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "request", "requestId": id, "method": method, "params": map[string]any{"sessionId": session}})
	return raw
}

func TestRelayInboundSchedulerPrioritizesAcrossSessionsButPreservesSessionFIFO(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	order := []string{}
	s := newRelayInboundScheduler(func(msg WireMessage) {
		if msg.RequestID == "running" {
			close(started)
			<-release
		}
		mu.Lock()
		order = append(order, msg.RequestID)
		mu.Unlock()
	}, nil)
	defer s.close()
	if err := s.enqueue(inboundRequest("running", "get_session_messages", "running-session")); err != nil {
		t.Fatal(err)
	}
	<-started
	// Same-session metadata cannot cross its earlier bulk head.
	if err := s.enqueue(inboundRequest("bulk-a", "get_session_messages", "same")); err != nil {
		t.Fatal(err)
	}
	if err := s.enqueue(inboundRequest("metadata-a", "list_models", "same")); err != nil {
		t.Fatal(err)
	}
	// A different session's metadata is eligible immediately after the running handler.
	if err := s.enqueue(inboundRequest("metadata-b", "list_models", "other")); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n == 4 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	want := []string{"running", "metadata-b", "bulk-a", "metadata-a"}
	if len(got) != len(want) {
		t.Fatalf("dispatch order = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dispatch order = %v, want %v", got, want)
		}
	}
}

func TestRelayInboundSchedulerSupersedesPendingHistoryAndBoundsBytes(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	dispatched := make(chan string, 4)
	s := newRelayInboundScheduler(func(msg WireMessage) {
		if msg.RequestID == "running" {
			close(started)
			<-release
		}
		dispatched <- msg.RequestID
	}, nil)
	defer s.close()
	if err := s.enqueue(inboundRequest("running", "send_message", "blocker")); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := s.enqueue(inboundRequest("old", "get_session_messages", "same")); err != nil {
		t.Fatal(err)
	}
	if err := s.enqueue(inboundRequest("new", "get_session_messages", "same")); err != nil {
		t.Fatal(err)
	}
	tooLarge := make(json.RawMessage, relayInboundQueueBytes+1)
	if err := s.enqueue(tooLarge); err == nil {
		t.Fatal("oversize inbound payload must be rejected")
	}
	close(release)
	if got := <-dispatched; got != "running" {
		t.Fatalf("first = %q", got)
	}
	if got := <-dispatched; got != "new" {
		t.Fatalf("pending history was not superseded, got %q", got)
	}
	select {
	case got := <-dispatched:
		t.Fatalf("unexpected stale dispatch %q", got)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRelayInboundSchedulersDoNotBlockAcrossDevices(t *testing.T) {
	aStarted := make(chan struct{})
	releaseA := make(chan struct{})
	bDone := make(chan struct{})
	a := newRelayInboundScheduler(func(WireMessage) { close(aStarted); <-releaseA }, nil)
	b := newRelayInboundScheduler(func(WireMessage) { close(bDone) }, nil)
	defer a.close()
	defer b.close()
	if err := a.enqueue(inboundRequest("a", "get_session_messages", "a")); err != nil {
		t.Fatal(err)
	}
	<-aStarted
	if err := b.enqueue(inboundRequest("b", "list_models", "b")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-bDone:
	case <-time.After(time.Second):
		t.Fatal("device B was blocked by device A handler")
	}
	close(releaseA)
}

func TestSessionBulkGenerationAtomicInstall(t *testing.T) {
	rc := NewRelayDeviceConn("device", "bridge", "route", 1, nil, make([]byte, 32), nil, func(json.RawMessage) error { return nil })
	gen1 := rc.advanceSessionBulkGeneration("session", "old")
	old := newOutboundBulkHandle("old-group")
	if !rc.installHandleIfSessionBulkGenerationCurrent("session", gen1, old) {
		t.Fatal("initial handle install failed")
	}
	gen2 := rc.advanceSessionBulkGeneration("session", "new")
	if !old.Cancelled() {
		t.Fatal("old active handle was not cancelled")
	}
	if rc.installHandleIfSessionBulkGenerationCurrent("session", gen1, newOutboundBulkHandle("stale")) {
		t.Fatal("stale generation installed")
	}
	fresh := newOutboundBulkHandle("new-group")
	if !rc.installHandleIfSessionBulkGenerationCurrent("session", gen2, fresh) {
		t.Fatal("current generation did not install")
	}
	rc.completeBulkHandle("session", fresh)
	rc.mu.Lock()
	leaked := rc.activeBulkHandles["session"]
	rc.mu.Unlock()
	if leaked != nil {
		t.Fatal("completed handle leaked")
	}
}

func TestSessionBulkGenerationConcurrentAdvanceAndInstall(t *testing.T) {
	for range 200 {
		rc := NewRelayDeviceConn("device", "bridge", "route", 1, nil, make([]byte, 32), nil, func(json.RawMessage) error { return nil })
		gen := rc.advanceSessionBulkGeneration("session", "old")
		handle := newOutboundBulkHandle("old-group")
		start := make(chan struct{})
		installed := make(chan bool, 1)
		done := make(chan struct{})
		go func() { <-start; installed <- rc.installHandleIfSessionBulkGenerationCurrent("session", gen, handle) }()
		go func() { <-start; rc.advanceSessionBulkGeneration("session", "new"); close(done) }()
		close(start)
		wasInstalled := <-installed
		<-done
		rc.mu.Lock()
		active := rc.activeBulkHandles["session"]
		current := rc.sessionBulkGenerations["session"]
		rc.mu.Unlock()
		if current != gen+1 {
			t.Fatalf("generation = %d, want %d", current, gen+1)
		}
		if active == handle {
			t.Fatal("stale handle became active")
		}
		if wasInstalled && !handle.Cancelled() {
			t.Fatal("installed old handle was not cancelled by advance")
		}
	}
}
