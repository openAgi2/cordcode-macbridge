package codex

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestPassiveSubscriber_Close_NoPanicOnLateSend verifies the T03 invariant for
// the codex passive subscriber: after Close()'s timeout branch fires, a producer
// that sends to events must NOT panic. Pre-fix, Close closed events directly
// after the timeout. Post-fix, the close is deferred until wg exits.
func TestPassiveSubscriber_Close_NoPanicOnLateSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &passiveSubscriber{
		events: make(chan core.Event, 1),
		ctx:    ctx,
		cancel: cancel,
	}

	// Slow producer: holds wg, sends an event AFTER Close's 3s timeout.
	s.wg.Add(1)
	producerDone := make(chan struct{})
	go func() {
		defer s.wg.Done()
		defer close(producerDone)
		time.Sleep(3*time.Second + 200*time.Millisecond)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("passive producer panicked on late send: %v", r)
				}
			}()
			select {
			case s.events <- core.Event{Type: core.EventError}:
			case <-time.After(time.Second):
			}
		}()
	}()

	done := make(chan struct{})
	go func() {
		_ = s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("passive Close() did not return within 6s")
	}
	select {
	case <-producerDone:
	case <-time.After(6 * time.Second):
		t.Fatal("passive producer did not finish")
	}
	select {
	case _, ok := <-s.events:
		if ok {
			for range s.events {
			}
		}
	case <-time.After(6 * time.Second):
		t.Fatal("passive events channel never closed after producer exit")
	}
}

// TestPassiveSubscriber_Close_Idempotent verifies repeated Close is safe.
func TestPassiveSubscriber_Close_Idempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &passiveSubscriber{
		events: make(chan core.Event, 1),
		ctx:    ctx,
		cancel: cancel,
	}
	for i := 0; i < 3; i++ {
		if err := s.Close(); err != nil {
			t.Fatalf("Close[%d] error: %v", i, err)
		}
	}
	select {
	case _, ok := <-s.events:
		if ok {
			t.Fatal("passive events still open after Close")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("passive events not closed")
	}
}

// TestAppServerSession_Close_NoPanicOnLateSend verifies the same invariant for
// the codex app-server session Close path.
func TestAppServerSession_Close_NoPanicOnLateSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &appServerSession{
		events: make(chan core.Event, 1),
		ctx:    ctx,
		cancel: cancel,
	}
	s.alive.Store(true)

	s.wg.Add(1)
	producerDone := make(chan struct{})
	go func() {
		defer s.wg.Done()
		defer close(producerDone)
		// App-server close timeout is 2s.
		time.Sleep(2*time.Second + 200*time.Millisecond)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("appserver producer panicked on late send: %v", r)
				}
			}()
			select {
			case s.events <- core.Event{Type: core.EventError}:
			case <-time.After(time.Second):
			}
		}()
	}()

	done := make(chan struct{})
	go func() {
		_ = s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("appserver Close() did not return within 6s")
	}
	select {
	case <-producerDone:
	case <-time.After(6 * time.Second):
		t.Fatal("appserver producer did not finish")
	}
	select {
	case _, ok := <-s.events:
		if ok {
			for range s.events {
			}
		}
	case <-time.After(6 * time.Second):
		t.Fatal("appserver events channel never closed after producer exit")
	}
}

// TestAppServerSession_Close_Idempotent verifies repeated Close is safe.
func TestAppServerSession_Close_Idempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &appServerSession{
		events: make(chan core.Event, 1),
		ctx:    ctx,
		cancel: cancel,
	}
	s.alive.Store(true)
	for i := 0; i < 3; i++ {
		if err := s.Close(); err != nil {
			t.Fatalf("Close[%d] error: %v", i, err)
		}
	}
	select {
	case _, ok := <-s.events:
		if ok {
			t.Fatal("appserver events still open after Close")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("appserver events not closed")
	}
}
