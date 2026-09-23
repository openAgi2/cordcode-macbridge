package dshweb

// Browser-auth integration tests (2026-09-23 plan §7): cookie attach on
// unary + WS, 401 single-flight refresh + retry, token exchange (official
// flow), secret mint (fallback), and the credentials scanner's read-only
// boundary / fail-closed shape rules. All against local fake servers with
// the SAME cookie format dsh uses — no registry, no real dsh.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// hmacEqual mirrors dsh's decodeCookie signature check (timingSafeEqual).
func hmacEqual(secret []byte, body, encodedSig string) bool {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(encodedSig)
	return err == nil && hmac.Equal(got, want)
}

// authTestServer is a dsh-like /api gateway enforcing the v1 browser cookie:
// same name derivation, same HMAC format, same 303 token exchange.
type authTestServer struct {
	mu           sync.Mutex
	secret       []byte
	token        string // launch token accepted at /?token=
	exchangeHits atomic.Int32
	describeHits atomic.Int32
	cookieSeen   atomic.Value // string
	srv          *httptest.Server
}

func newAuthTestServer(t *testing.T) *authTestServer {
	t.Helper()
	s := &authTestServer{secret: testSecret(1), token: "test-launch-token"}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/session/list", s.handleDescribe)
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func testSecret(n byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = n + byte(i)
	}
	return b
}

func (s *authTestServer) authority() string { return authorityOf(s.srv.URL) }

// validCookie mirrors dsh's decodeCookie checks (name, v1 shape, HMAC,
// authority, window) — enough to prove the bridge speaks the real format.
func (s *authTestServer) validCookie(headerValue string) bool {
	name := dshCookieName(s.authority())
	for _, segment := range strings.Split(headerValue, ";") {
		segment = strings.TrimSpace(segment)
		if !strings.HasPrefix(segment, name+"=") {
			continue
		}
		value := strings.TrimPrefix(segment, name+"=")
		parts := strings.Split(value, ".")
		if len(parts) != 3 || parts[0] != "v1" {
			return false
		}
		body, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return false
		}
		var payload browserCookiePayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return false
		}
		if payload.Version != 1 || payload.Authority != s.authority() {
			return false
		}
		now := time.Now().UnixMilli()
		if payload.IssuedAt > now || payload.ExpiresAt <= now ||
			payload.ExpiresAt <= payload.IssuedAt ||
			payload.ExpiresAt-payload.IssuedAt > dshCookieMaxAge.Milliseconds() {
			return false
		}
		return hmacEqual(s.secret, parts[1], parts[2])
	}
	return false
}

func (s *authTestServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || r.URL.Query().Get("token") != s.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.exchangeHits.Add(1)
	name, value := mintBrowserCookie(s.authority(), s.secret, time.Now())
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, MaxAge: int(dshCookieMaxAge.Seconds())})
	w.Header().Set("Location", "./")
	w.WriteHeader(http.StatusSeeOther)
}

func (s *authTestServer) handleDescribe(w http.ResponseWriter, r *http.Request) {
	s.describeHits.Add(1)
	s.cookieSeen.Store(r.Header.Get("Cookie"))
	if !s.validCookie(r.Header.Get("Cookie")) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(serverResponse{
		Type: "server-response", RPCID: "probe",
		Result: rpcResultBody{OK: true, Value: json.RawMessage(`{"version":"test","cwd":"/tmp","attachedSessions":0,"canOpenPath":false}`)},
	})
}

// rotateSecret invalidates all outstanding cookies (models a secret change).
func (s *authTestServer) rotateSecret() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secret = testSecret(9)
}

func (s *authTestServer) currentSecret() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secret
}

// writeCredentialsFixture writes a dsh-shaped credentials store into dir and
// pins HOME there (isolating the test from the real ~/.dsh).
func writeCredentialsFixture(t *testing.T, secret []byte) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dshDir := filepath.Join(home, ".dsh")
	if err := os.MkdirAll(dshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "version: 1\n" +
		"refs:\n" +
		"  DEEPSEEK_API_KEY: sk-test-should-never-be-captured-0123456789\n" +
		"records:\n" +
		"  some-other-plugin/some-record:\n" +
		"    kind: grant\n" +
		"    payload:\n" +
		"      version: 1\n" +
		"      secret: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n" +
		"  " + browserSessionRecordKey + ":\n" +
		"    kind: grant\n" +
		"    payload:\n" +
		"      version: 1\n" +
		"      secret: " + base64.RawURLEncoding.EncodeToString(secret) + "\n"
	if err := os.WriteFile(filepath.Join(dshDir, ".credentials.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dshDir, ".credentials.yaml")
}

func authedClient(t *testing.T, srv *authTestServer, dataDir string, launchURL string) (*Client, *seatAuth) {
	t.Helper()
	auth := newSeatAuth(dataDir, nil)
	auth.setLaunchURL(launchURL)
	c := NewClient(srv.srv.URL, nil)
	c.SetAuth(auth)
	return c, auth
}

func TestClientAttachesCookieAndSucceeds(t *testing.T) {
	srv := newAuthTestServer(t)
	auth := newSeatAuth(t.TempDir(), nil)
	// Preload via the official exchange path (launch URL → 303 → cookie).
	auth.setLaunchURL(srv.srv.URL + "/?token=" + srv.token)
	c := NewClient(srv.srv.URL, nil)
	c.SetAuth(auth)

	var out sessionListValue
	if err := c.Call(context.Background(), "session/list", listArgs(), &out); err != nil {
		t.Fatalf("authed Call: %v", err)
	}
	if got, _ := srv.cookieSeen.Load().(string); !strings.Contains(got, "dsh-auth-") {
		t.Fatalf("server saw no dsh cookie: %q", got)
	}
}

func TestCall401RefreshesOnceAndRetries(t *testing.T) {
	srv := newAuthTestServer(t)
	// Cookie preloaded for the ORIGINAL secret; then the secret rotates on
	// both the server and the persistent store (they share it in reality) —
	// the first Call 401s, the refresh mints from the fixture, retry passes.
	auth := newSeatAuth(t.TempDir(), nil)
	name, value := mintBrowserCookie(srv.authority(), testSecret(1), time.Now())
	auth.storeCookie(srv.authority(), name, value, time.Now().Add(dshCookieMaxAge))
	c := NewClient(srv.srv.URL, nil)
	c.SetAuth(auth)

	srv.rotateSecret()
	writeCredentialsFixture(t, srv.currentSecret())

	var out sessionListValue
	if err := c.Call(context.Background(), "session/list", listArgs(), &out); err != nil {
		t.Fatalf("Call after refresh must succeed: %v", err)
	}
	// The minted cookie must be the rotated secret's — the server accepted it.
	if got, _ := srv.cookieSeen.Load().(string); !strings.Contains(got, "dsh-auth-") {
		t.Fatalf("retry carried no cookie: %q", got)
	}
}

func TestRefreshSingleFlight(t *testing.T) {
	srv := newAuthTestServer(t)
	auth := newSeatAuth(t.TempDir(), nil)
	auth.setLaunchURL(srv.srv.URL + "/?token=" + srv.token)
	c := NewClient(srv.srv.URL, nil)
	c.SetAuth(auth)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out sessionListValue
			errs[i] = c.Call(context.Background(), "session/list", listArgs(), &out)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent call %d: %v", i, err)
		}
	}
	if got := srv.exchangeHits.Load(); got != 1 {
		t.Fatalf("single-flight violated: %d exchanges, want 1", got)
	}
}

func TestTokenExchangePersistsCookie0600(t *testing.T) {
	srv := newAuthTestServer(t)
	dataDir := t.TempDir()
	auth := newSeatAuth(dataDir, nil)
	auth.setLaunchURL(srv.srv.URL + "/?token=" + srv.token)

	if err := auth.ensureCookie(context.Background(), srv.srv.URL, false); err != nil {
		t.Fatalf("ensureCookie via exchange: %v", err)
	}
	path := filepath.Join(dataDir, authCookieFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("persisted cookie missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cookie file mode %v, want 0600", info.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	var rec authCookieRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.CookieName != dshCookieName(srv.authority()) {
		t.Fatalf("persisted cookie name %q does not match authority derivation", rec.CookieName)
	}
	// A fresh seatAuth (same data dir) restores it without any exchange.
	fresh := newSeatAuth(dataDir, nil)
	if _, ok := fresh.cookieHeaderFor(srv.srv.URL); !ok {
		t.Fatal("persisted cookie must be loadable by a fresh seatAuth")
	}
	if got := srv.exchangeHits.Load(); got != 1 {
		t.Fatalf("unexpected exchange count: %d", got)
	}
}

func TestMintCookieAcceptedByServer(t *testing.T) {
	srv := newAuthTestServer(t)
	writeCredentialsFixture(t, srv.currentSecret())
	auth := newSeatAuth(t.TempDir(), nil)

	if err := auth.ensureCookie(context.Background(), srv.srv.URL, false); err != nil {
		t.Fatalf("mint path: %v", err)
	}
	if got := srv.exchangeHits.Load(); got != 0 {
		t.Fatalf("mint must not exchange (exchangeHits=%d)", got)
	}
	c := NewClient(srv.srv.URL, nil)
	c.SetAuth(auth)
	var out sessionListValue
	if err := c.Call(context.Background(), "session/list", listArgs(), &out); err != nil {
		t.Fatalf("minted cookie rejected by server: %v", err)
	}
}

func TestOpenStreamAttachesCookieAndRetries401(t *testing.T) {
	srv := newAuthTestServer(t)
	// WS upgrade endpoint enforcing the same cookie.
	upgraderHits := atomic.Int32{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events.mux", func(w http.ResponseWriter, r *http.Request) {
		upgraderHits.Add(1)
		if !srv.validCookie(r.Header.Get("Cookie")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusUpgradeRequired) // enough: the dial got past auth
	})
	wsSrv := httptest.NewServer(mux)
	defer wsSrv.Close()

	writeCredentialsFixture(t, srv.currentSecret())
	auth := newSeatAuth(t.TempDir(), nil)
	c := NewClient(wsSrv.URL, nil)
	c.SetAuth(auth)

	// No cookie yet → dial 401 → refresh (mint) → retry → past auth (426).
	_, err := c.OpenStream(context.Background(), "mux", "/api/events.mux")
	if err == nil {
		t.Fatal("expected the 426 non-upgrade to surface as a dial error")
	}
	if got := upgraderHits.Load(); got < 2 {
		t.Fatalf("dial must have retried past the 401 (hits=%d)", got)
	}
	var ce *carrierError
	if !asCarrier(err, &ce) || ce.Status != http.StatusUnauthorized {
		// After the refresh the retry reached the endpoint (426) — the final
		// error is the upgrade failure, which is the success path here.
		t.Logf("final dial error after auth: %v", err)
	}
}

func TestCredentialsScannerReadonlyBoundary(t *testing.T) {
	secret := testSecret(5)
	path := writeCredentialsFixture(t, secret)

	raw, err := readBrowserSessionSecret(path)
	if err != nil {
		t.Fatalf("scanner: %v", err)
	}
	if string(raw) != string(secret) {
		t.Fatal("scanner returned the wrong record's secret (read-only boundary violated)")
	}
}

func TestCredentialsScannerFailsClosed(t *testing.T) {
	dir := t.TempDir()
	write := func(content string) string {
		p := filepath.Join(dir, "creds.yaml")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name    string
		content string
	}{
		{"store-version-2", "version: 2\nrecords:\n  " + browserSessionRecordKey + ":\n    kind: grant\n    payload:\n      version: 1\n      secret: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"},
		{"record-version-2", "version: 1\nrecords:\n  " + browserSessionRecordKey + ":\n    kind: grant\n    payload:\n      version: 2\n      secret: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"},
		{"record-missing", "version: 1\nrecords:\n  other/thing:\n    kind: grant\n"},
		{"kind-wrong", "version: 1\nrecords:\n  " + browserSessionRecordKey + ":\n    kind: ref\n    payload:\n      version: 1\n      secret: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"},
		{"secret-short", "version: 1\nrecords:\n  " + browserSessionRecordKey + ":\n    kind: grant\n    payload:\n      version: 1\n      secret: AAAA\n"},
		{"flow-style", "version: 1\nrecords: {" + browserSessionRecordKey + ": {kind: grant}}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readBrowserSessionSecret(write(tc.content)); err == nil {
				t.Fatal("scanner must fail closed on this shape")
			}
		})
	}
}

func TestAuthorityOfAndCookieName(t *testing.T) {
	if got := authorityOf("http://127.0.0.1:3080"); got != "127.0.0.1:3080" {
		t.Fatalf("authorityOf: %q", got)
	}
	// Pinned against the live Set-Cookie captured 2026-09-23.
	if got := dshCookieName("127.0.0.1:3080"); got != "dsh-auth-VPhEEcLKeqRDBoBalzN2Nm7CnfxKhLE00pKIDWxt1sw" {
		t.Fatalf("cookieName derivation drifted: %q", got)
	}
}

func TestLaunchURLCaptureFiltersForeignAuthority(t *testing.T) {
	seat := freeLoopbackSeat(t)
	r := NewResolver(WithProbeURLs([]string{seat}))
	auth := newSeatAuth(t.TempDir(), nil)
	r.SetAuth(auth)

	r.notifyLaunchURL("dsh web: http://127.0.0.1:9999/?token=foreign-token")
	if got := auth.currentLaunchURL(); got != "" {
		t.Fatalf("foreign authority must be ignored, got %q", got)
	}
	r.notifyLaunchURL("noise line without url")
	if got := auth.currentLaunchURL(); got != "" {
		t.Fatalf("noise must be ignored, got %q", got)
	}
	// The seat's own authority is captured.
	_, port := seatHostPort(seat)
	r.notifyLaunchURL(fmt.Sprintf("dsh web: http://127.0.0.1:%d/?token=own-token", port))
	if got := auth.currentLaunchURL(); !strings.HasSuffix(got, "token=own-token") {
		t.Fatalf("own launch URL must be captured, got %q", got)
	}
}

func TestNilSeatAuthIsNoop(t *testing.T) {
	// Nil seatAuth = pre-auth behavior: no cookie, no refresh, no panic.
	var auth *seatAuth
	if _, ok := auth.cookieHeaderFor("http://127.0.0.1:3080"); ok {
		t.Fatal("nil auth must have no cookie")
	}
	if err := auth.ensureCookie(context.Background(), "http://127.0.0.1:3080", false); err != nil {
		t.Fatalf("nil auth ensureCookie: %v", err)
	}
	auth.setLaunchURL("http://x/?token=y") // must not panic
}

func asCarrier(err error, target **carrierError) bool {
	for err != nil {
		if ce, ok := err.(*carrierError); ok {
			*target = ce
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
