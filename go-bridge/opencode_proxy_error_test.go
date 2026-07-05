package gobridge

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenCodeProxyHTTPErrorDistinguishability verifies the typed-error contract that the
// list_pinned_sessions handler relies on (R5-D1): a definitive upstream 404 (prune the pin)
// must be distinguishable from a 5xx transient failure (fail the RPC) and from a network /
// timeout error, WITHOUT parsing error message text.
func TestOpenCodeProxyHTTPErrorDistinguishability(t *testing.T) {
	t.Run("404 is NotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"session not found"}`))
		}))
		defer srv.Close()

		p := NewOpenCodeProxy(srv.URL, "", "")
		_, err := p.fetch("/session/abc")
		if err == nil {
			t.Fatal("expected error for 404, got nil")
		}
		if !IsOpenCodeNotFound(err) {
			t.Fatalf("IsOpenCodeNotFound(404) = false; want true. err=%T %v", err, err)
		}
		httpErr, ok := err.(*OCHTTPError)
		if !ok {
			t.Fatalf("expected *OCHTTPError, got %T", err)
		}
		if httpErr.Code != http.StatusNotFound {
			t.Fatalf("Code=%d, want 404", httpErr.Code)
		}
		if httpErr.Error() == "" {
			t.Fatal("Error() message must not be empty (used in logs)")
		}
	})

	t.Run("500 is NOT NotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"boom"}`))
		}))
		defer srv.Close()

		p := NewOpenCodeProxy(srv.URL, "", "")
		_, err := p.fetch("/session/abc")
		if err == nil {
			t.Fatal("expected error for 500, got nil")
		}
		if IsOpenCodeNotFound(err) {
			t.Fatalf("IsOpenCodeNotFound(500) = true; want false. err=%v", err)
		}
		httpErr, ok := err.(*OCHTTPError)
		if !ok {
			t.Fatalf("expected *OCHTTPError for 500, got %T", err)
		}
		if httpErr.Code != http.StatusInternalServerError {
			t.Fatalf("Code=%d, want 500", httpErr.Code)
		}
	})

	t.Run("network error is NOT NotFound and NOT OCHTTPError", func(t *testing.T) {
		// A closed server yields a connection-refused error (a *url.Error wrapping a net.OpError),
		// which is the same shape as a real timeout / unreachable-host failure.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		addr := srv.URL
		srv.Close()

		p := NewOpenCodeProxy(addr, "", "")
		_, err := p.fetch("/session/abc")
		if err == nil {
			t.Fatal("expected network error, got nil")
		}
		if IsOpenCodeNotFound(err) {
			t.Fatalf("IsOpenCodeNotFound(network err) = true; want false. err=%v", err)
		}
		if _, ok := err.(*OCHTTPError); ok {
			t.Fatalf("network error must not be *OCHTTPError; got one: %v", err)
		}
	})

	t.Run("wrapped 404 still recognized via errors.As", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`not found`))
		}))
		defer srv.Close()

		p := NewOpenCodeProxy(srv.URL, "", "")
		_, err := p.fetch("/session/abc")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// Simulate a caller wrapping the error with %%w; IsOpenCodeNotFound must still see it.
		wrapped := wrapErr(err)
		if !IsOpenCodeNotFound(wrapped) {
			t.Fatalf("IsOpenCodeNotFound(wrapped 404) = false; want true. wrapped=%v", wrapped)
		}
	})
}

// wrapErr mirrors fmt.Errorf("...: %w", err) without pulling fmt into the test file's main
// imports unnecessarily — it exercises the errors.As chain in IsOpenCodeNotFound.
func wrapErr(err error) error { return wrappedErr{cause: err} }

type wrappedErr struct{ cause error }

func (w wrappedErr) Error() string { return "wrapped: " + w.cause.Error() }
func (w wrappedErr) Unwrap() error { return w.cause }
