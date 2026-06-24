package opencode

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestOpencodeSession_Close_NoPanicOnLateSend verifies the T03 invariant:
// after Close()'s timeout branch fires (producer still holding wg), a producer
// that sends to events must NOT panic. Pre-fix, Close closed events directly
// after the timeout even while a producer might still send → panic. Post-fix,
// events is closed only after the producer (wg) exits.
func TestOpencodeSession_Close_NoPanicOnLateSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &opencodeSession{
		events:       make(chan core.Event, 4),
		ctx:          ctx,
		cancel:       cancel,
		closeTimeout: 50 * time.Millisecond, // exercise timeout branch quickly
	}
	s.alive.Store(true)

	// Producer holds wg longer than closeTimeout, then sends an event (must not
	// panic), then releases wg.
	s.wg.Add(1)
	producerDone := make(chan struct{})
	go func() {
		defer s.wg.Done()
		defer close(producerDone)
		time.Sleep(300 * time.Millisecond) // longer than closeTimeout
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("producer panicked on late send: %v", r)
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
	case <-time.After(3 * time.Second):
		t.Fatal("Close() did not return within 3s (timeout branch should fire)")
	}

	select {
	case <-producerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("producer did not finish")
	}
	// events must eventually close once wg exits.
	select {
	case _, ok := <-s.events:
		if ok {
			for range s.events {
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("events channel never closed after producer exit")
	}
}

// TestOpencodeSession_Close_Idempotent verifies Close can be called multiple
// times without panic (closeOnce guards the single close).
func TestOpencodeSession_Close_Idempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &opencodeSession{
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
	// events should be closed exactly once and readable as closed.
	select {
	case _, ok := <-s.events:
		if ok {
			t.Fatal("events still open after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events not closed")
	}
}

// TestSSESubscriber_Close_NoPanicOnLateSend verifies the same invariant for
// the SSE subscriber path.
func TestSSESubscriber_Close_NoPanicOnLateSend(t *testing.T) {
	sub := newTestSSESubscriber()
	sub.wg.Add(1)
	producerDone := make(chan struct{})
	go func() {
		defer sub.wg.Done()
		defer close(producerDone)
		time.Sleep(3*time.Second + 200*time.Millisecond)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("SSE producer panicked on late send: %v", r)
				}
			}()
			select {
			case sub.events <- core.Event{Type: core.EventError}:
			case <-time.After(time.Second):
			}
		}()
	}()

	done := make(chan struct{})
	go func() {
		_ = sub.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE Close() did not return within 5s")
	}
	select {
	case <-producerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE producer did not finish")
	}
	select {
	case _, ok := <-sub.events:
		if ok {
			for range sub.events {
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSE events channel never closed after producer exit")
	}
}

// TestSSESubscriber_Close_Idempotent verifies repeated Close is safe.
func TestSSESubscriber_Close_Idempotent(t *testing.T) {
	sub := newTestSSESubscriber()
	for i := 0; i < 3; i++ {
		if err := sub.Close(); err != nil {
			t.Fatalf("Close[%d] error: %v", i, err)
		}
	}
	select {
	case _, ok := <-sub.events:
		if ok {
			t.Fatal("SSE events still open after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSE events not closed")
	}
}

// compile-time guard: ensure sync is used in this test file.
var _ = sync.WaitGroup{}
