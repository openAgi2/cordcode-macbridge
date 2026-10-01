package dshweb

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type heldProbeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f heldProbeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHeldSeatRequestFailuresDoNotTerminateTurn(t *testing.T) {
	for _, kind := range []string{
		"rpc_error", "unauthorized", "forbidden", "http_error", "bad_response",
		"caller_canceled", "caller_deadline", "probe_timeout", "read_eof", "read_reset",
	} {
		t.Run(kind, func(t *testing.T) {
			var fail atomic.Bool
			stopHandler := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if !fail.Load() {
					describeHandler(w, req)
					return
				}
				switch kind {
				case "rpc_error":
					writeServerResponse(w, "probe", rpcResultBody{Error: &RPCError{
						Code: "gateway/internal", Message: "controlled list failure",
					}})
				case "unauthorized":
					w.WriteHeader(http.StatusUnauthorized)
				case "forbidden":
					w.WriteHeader(http.StatusForbidden)
				case "http_error":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "bad_response":
					_, _ = w.Write([]byte("invalid JSON"))
				case "probe_timeout":
					select {
					case <-req.Context().Done():
					case <-stopHandler:
					}
				default:
					describeHandler(w, req)
				}
			}))
			defer server.Close()
			defer close(stopHandler)
			client := &http.Client{}
			resolver := NewResolver(WithProbeURLs([]string{server.URL}), WithHTTPClient(client))
			inst, err := resolver.Resolve(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			fail.Store(true)
			a := &Agent{resolver: resolver}
			session := newBoundSession("request-failure-session")
			a.bindings.put(session.CurrentSessionID(), session)
			a.running.setOne(session.CurrentSessionID(), true)
			ctx := context.Background()
			switch kind {
			case "caller_canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "caller_deadline":
				expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				ctx = expired
			case "probe_timeout":
				client.Timeout = 30 * time.Millisecond
			case "read_eof", "read_reset":
				cause := error(io.EOF)
				if kind == "read_reset" {
					cause = &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
				}
				client.Transport = heldProbeRoundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, cause
				})
			}
			_, err = resolver.Resolve(ctx)
			if err == nil {
				t.Fatal("request failure must remain visible")
			}
			var reconnecting *ErrInstanceReconnecting
			if errors.As(err, &reconnecting) {
				t.Fatalf("request error was replaced by instance loss: %v", err)
			}
			if kind == "rpc_error" {
				var rpc *RPCError
				if !errors.As(err, &rpc) || rpc.Code != "gateway/internal" || rpc.Message != "controlled list failure" {
					t.Fatalf("original RPC error not preserved: %v", err)
				}
			}
			if resolver.Current() != inst || resolver.LossSeq() != 0 {
				t.Fatal("request failure changed held instance/loss generation")
			}
			if grace, _ := resolver.GraceState(); grace {
				t.Fatal("request failure entered loss grace")
			}
			a.handleSeatLost() // Must have no loss edge to convert into a terminal.
			if events := drainEvents(session.events); len(events) != 0 {
				t.Fatalf("still-running turn received a terminal: %+v", events)
			}
			fail.Store(false)
			client.Timeout = 0
			client.Transport = nil
			if recovered, err := resolver.Resolve(context.Background()); err != nil || recovered != inst {
				t.Fatalf("next request must use the same held instance: %v %v", recovered, err)
			}
		})
	}
}

func TestConcurrentHeldSeatConnectionRefusalHasOneLossEdge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(describeHandler))
	defer server.Close()
	client := &http.Client{}
	resolver := NewResolver(WithProbeURLs([]string{server.URL}), WithHTTPClient(client))
	if _, err := resolver.Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	client.Transport = heldProbeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-release
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	})
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := resolver.Resolve(context.Background())
			errorsSeen <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			wg.Wait()
			t.Fatal("concurrent probes did not reach the barrier")
		}
	}
	close(release)
	wg.Wait()
	for i := 0; i < 2; i++ {
		var reconnecting *ErrInstanceReconnecting
		if err := <-errorsSeen; !errors.As(err, &reconnecting) {
			t.Fatalf("confirmed refusal must return loss grace: %v", err)
		}
	}
	if resolver.LossSeq() != 1 || resolver.Current() != nil {
		t.Fatalf("one lost held instance produced %d edges", resolver.LossSeq())
	}
}

func TestLateHeldProbeRefusalDoesNotLoseReboundInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(describeHandler))
	defer server.Close()
	client := &http.Client{}
	r := NewResolver(WithProbeURLs([]string{server.URL}), WithHTTPClient(client))
	old, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	client.Transport = heldProbeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	})
	done := make(chan error, 1)
	go func() { _, err := r.Resolve(context.Background()); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("probe did not reach barrier")
	}
	newInstance := &ResolvedInstance{BaseURL: old.BaseURL, Port: old.Port, Source: SourceExternal}
	r.mu.Lock()
	r.rebindLocked(newInstance, "test-rebound")
	r.mu.Unlock()
	close(release)
	if err := <-done; err == nil {
		t.Fatal("stale failed request must remain an error")
	}
	if r.Current() != newInstance || r.LossSeq() != 0 {
		t.Fatal("late refusal poisoned the rebound instance")
	}
}

func TestHeldSeatNegativeCacheCannotCreateLoss(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(describeHandler))
	defer server.Close()
	r := NewResolver(WithProbeURLs([]string{server.URL}))
	inst, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.negUntil = time.Now().Add(time.Second)
	r.mu.Unlock()
	if got, err := r.Resolve(context.Background()); err != nil || got != inst {
		t.Fatalf("cache deadline substituted for a live probe: %v %v", got, err)
	}
	if r.LossSeq() != 0 {
		t.Fatal("cache deadline created a loss edge")
	}
}
