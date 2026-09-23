package dshweb

// Browser-auth integration for dsh web instances (2026-09-23 plan
// docs/2026-09-23-dsh-web-auth-integration-plan.md). dsh ≥0.1.6-alpha requires
// a signed browser-session cookie on every /api request (unary + WS upgrade);
// the v1 no-auth contract the backend was built on is gone from all current
// releases. This file gives the bridge the same login the official browser
// flow gets, WITHOUT touching dsh itself (owner red line):
//
//   1. cookie persistence — one 30-day signed cookie per seat authority,
//      stored in the bridge data dir (0600). The signing secret lives in
//      dsh's own persistent credential store, so the cookie survives dsh
//      restarts; the per-launch token does not matter day to day.
//   2. token exchange (official flow, automated) — when the bridge spawns
//      dsh itself, the launch URL (with ?token=) is captured from the child's
//      stdout and exchanged for the cookie, exactly like the browser does.
//   3. secret mint (fallback) — when the seat is externally held (user's own
//      terminal) there is no stdout to read; the cookie is minted locally
//      from the persistent secret, read-only, one record only.
//
// Format facts are pinned against the installed 0.1.7-alpha.1 bundle
// (dsh-client-connection/lib/index.js) and live samples captured 2026-09-23
// (token line on stdout, 303 exchange, Set-Cookie, ~/.dsh/.credentials.yaml
// shape); upstream source: packages/client/connection/src/browser-auth.ts
// @ 0d1f500. Cookie format is v1 only — any drift fails closed, no variant
// guessing.

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// authCookieFile persists the seat's browser-session cookie (0600; the cookie
// is a 30-day login credential — same protection as management-token files).
const authCookieFile = "dsh-web-auth-cookie.json"

const authCookieVersion = 1

// dshCookieMaxAge mirrors the official cookie lifetime (observed Max-Age
// 2592000s = 30 days; browser-auth.ts maxAgeDays default). The server rejects
// cookies whose issuedAt→expiresAt window exceeds its configured maxAge, so
// this must not exceed the official value.
const dshCookieMaxAge = 30 * 24 * time.Hour

// browserSessionRecordKey is the single credential record this package reads.
const browserSessionRecordKey = "client-connection/browser-session"

// dshCredentialsPath is dsh's own persistent credential store (read-only,
// one record only — the file also holds the user's API keys under refs/,
// which must never be captured, cached, or logged).
func dshCredentialsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".dsh", ".credentials.yaml")
}

// seatAuth owns the seat's browser-session cookie: persistence, single-flight
// refresh, token exchange, and secret minting. A nil *seatAuth is valid and
// means "no auth" (pre-auth dsh versions, tests) — every method tolerates it.
type seatAuth struct {
	dataDir string       // bridge data dir for the cookie file ("" = no persistence)
	hc      *http.Client // for the token exchange GET (nil = default)

	// launchURL is the most recent spawn's captured entry URL (with ?token=),
	// set by the exec starter's stdout scan; empty when the seat is external.
	launchMu   sync.Mutex
	launchURL  string
	launchFrom time.Time

	mu              sync.Mutex
	cookieAuthority string
	cookieName      string
	cookieValue     string
	expiresAt       time.Time
	refreshing      bool
	refreshDone     chan struct{}
	lastRefresh     error
	lastRefreshDone time.Time
}

// newSeatAuth loads any persisted cookie.
func newSeatAuth(dataDir string, hc *http.Client) *seatAuth {
	a := &seatAuth{dataDir: dataDir, hc: hc}
	a.loadCookie()
	return a
}

// authorityOf derives the dsh cookie authority (host:port) from a seat base URL.
func authorityOf(baseURL string) string {
	host, port := seatHostPort(baseURL)
	if port <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// cookieHeaderFor returns the Cookie header value for the seat when a cookie
// for exactly that authority exists and is unexpired.
func (a *seatAuth) cookieHeaderFor(seat string) (string, bool) {
	if a == nil {
		return "", false
	}
	authority := authorityOf(seat)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cookieValue == "" || a.cookieName == "" || a.cookieAuthority != authority ||
		!time.Now().Before(a.expiresAt) {
		return "", false
	}
	return a.cookieName + "=" + a.cookieValue, true
}

// setLaunchURL records the entry URL captured from a spawned dsh's stdout.
func (a *seatAuth) setLaunchURL(url string) {
	if a == nil {
		return
	}
	a.launchMu.Lock()
	a.launchURL = url
	a.launchFrom = time.Now()
	a.launchMu.Unlock()
}

// currentLaunchURL returns the captured entry URL if it is recent enough to
// belong to a live spawn of ours (the launch token dies with its process).
func (a *seatAuth) currentLaunchURL() string {
	if a == nil {
		return ""
	}
	a.launchMu.Lock()
	defer a.launchMu.Unlock()
	if a.launchURL == "" || time.Since(a.launchFrom) > 24*time.Hour {
		return ""
	}
	return a.launchURL
}

// ensureCookie makes sure a usable cookie for the seat exists. force=true is
// the 401 path: the server REJECTED the cookie we hold, so a refresh runs even
// though the cookie looks locally valid — a 1s completion window dedups the
// concurrent 401 storm onto the one refresh that already happened. Single-
// flight throughout: concurrent callers wait for the in-flight refresh.
func (a *seatAuth) ensureCookie(ctx context.Context, seat string, force bool) error {
	if a == nil {
		return nil
	}
	if !force {
		if _, ok := a.cookieHeaderFor(seat); ok {
			return nil
		}
	}
	authority := authorityOf(seat)
	if authority == "" {
		return fmt.Errorf("dshweb: seat %q has no authority to authenticate", seat)
	}

	a.mu.Lock()
	if force && !a.lastRefreshDone.IsZero() && time.Since(a.lastRefreshDone) < time.Second &&
		a.cookieAuthority == authority && a.cookieValue != "" && time.Now().Before(a.expiresAt) {
		// A refresh just produced this cookie; the caller retries with it.
		a.mu.Unlock()
		return nil
	}
	if a.refreshing {
		done := a.refreshDone
		a.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.cookieAuthority == authority && a.cookieValue != "" && time.Now().Before(a.expiresAt) {
			return nil
		}
		return a.lastRefresh
	}
	a.refreshing = true
	a.refreshDone = make(chan struct{})
	a.mu.Unlock()

	err := a.refresh(ctx, seat, authority)

	a.mu.Lock()
	a.lastRefresh = err
	a.lastRefreshDone = time.Now()
	a.refreshing = false
	close(a.refreshDone)
	a.mu.Unlock()
	return err
}

// refresh obtains a fresh cookie: token exchange first (official flow, when
// we spawned the seat), then the secret mint (external seats). Fail-closed.
func (a *seatAuth) refresh(ctx context.Context, seat, authority string) error {
	var exchangeErr error
	if url := a.currentLaunchURL(); url != "" {
		name, value, expiresAt, err := exchangeToken(ctx, a.hc, url, authority)
		if err == nil {
			a.storeCookie(authority, name, value, expiresAt)
			return nil
		}
		// A stale token from a dead child is expected; fall through to the
		// mint and keep the exchange failure for the combined error below.
		exchangeErr = err
	}
	secret, err := readBrowserSessionSecret(dshCredentialsPath())
	if err != nil {
		if exchangeErr != nil {
			return fmt.Errorf("dshweb: 无法获取 dsh web 登录态（token 交换失败: %v；读取凭据也失败: %w）", exchangeErr, err)
		}
		return fmt.Errorf("dshweb: 无法获取 dsh web 登录态（token 交换不可用，读取凭据失败）: %w", err)
	}
	name, value := mintBrowserCookie(authority, secret, time.Now())
	a.storeCookie(authority, name, value, time.Now().Add(dshCookieMaxAge))
	return nil
}

// storeCookie keeps the cookie in memory and persists it (0600).
func (a *seatAuth) storeCookie(authority, name, value string, expiresAt time.Time) {
	a.mu.Lock()
	a.cookieAuthority, a.cookieName, a.cookieValue, a.expiresAt = authority, name, value, expiresAt
	a.mu.Unlock()

	if a.dataDir == "" {
		return
	}
	rec := authCookieRecord{
		Version:     authCookieVersion,
		Authority:   authority,
		CookieName:  name,
		CookieValue: value,
		ExpiresAt:   expiresAt.UTC().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(a.dataDir, 0o700)
	_ = core.AtomicWriteFile(filepath.Join(a.dataDir, authCookieFile), b, 0o600)
}

type authCookieRecord struct {
	Version     int    `json:"version"`
	Authority   string `json:"authority"`
	CookieName  string `json:"cookie_name"`
	CookieValue string `json:"cookie_value"`
	ExpiresAt   string `json:"expires_at"`
}

// loadCookie restores a persisted cookie (authority is validated at use time
// by cookieHeaderFor).
func (a *seatAuth) loadCookie() {
	if a.dataDir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(a.dataDir, authCookieFile))
	if err != nil {
		return
	}
	var rec authCookieRecord
	if err := json.Unmarshal(b, &rec); err != nil || rec.Version != authCookieVersion {
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, rec.ExpiresAt)
	if err != nil || !time.Now().Before(expiresAt) {
		return
	}
	a.cookieAuthority, a.cookieName, a.cookieValue, a.expiresAt = rec.Authority, rec.CookieName, rec.CookieValue, expiresAt
}

// ── Token exchange (official browser flow) ───────────────────────────────────

// exchangeToken performs the official login step: GET the launch URL (no
// redirect follow), take the Set-Cookie from the 303, and validate the cookie
// name matches the seat authority's derivation.
func exchangeToken(ctx context.Context, hc *http.Client, launchURL, authority string) (name, value string, expiresAt time.Time, err error) {
	if hc == nil {
		hc = &http.Client{}
	}
	// Never follow the 303: the Set-Cookie on the exchange response is the
	// payload; following it would just fetch the index.
	noRedirect := *hc
	noRedirect.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, launchURL, nil)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("dshweb: token exchange request: %w", err)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("dshweb: token exchange: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		return "", "", time.Time{}, fmt.Errorf("dshweb: token exchange: HTTP %d", resp.StatusCode)
	}
	wantName := dshCookieName(authority)
	for _, cookie := range resp.Cookies() {
		if cookie.Name != wantName {
			continue
		}
		if cookie.Value == "" {
			return "", "", time.Time{}, fmt.Errorf("dshweb: token exchange: empty cookie value")
		}
		expiresAt = time.Now().Add(dshCookieMaxAge)
		if cookie.MaxAge > 0 {
			expiresAt = time.Now().Add(time.Duration(cookie.MaxAge) * time.Second)
		}
		return cookie.Name, cookie.Value, expiresAt, nil
	}
	return "", "", time.Time{}, fmt.Errorf("dshweb: token exchange: no %s cookie in response", wantName)
}

// ── Cookie format (v1, pinned against installed 0.1.7-alpha.1 + live sample) ─

// dshCookieName mirrors browser-auth.ts cookieName(authority):
// "dsh-auth-" + base64url(sha256(authority)). Verified against the live
// Set-Cookie of 2026-09-23 (dsh-auth-VPhEEcLKeqRDBoBalzN2Nm7CnfxKhLE00pKIDWxt1sw).
func dshCookieName(authority string) string {
	sum := sha256.Sum256([]byte(authority))
	return "dsh-auth-" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// browserCookiePayload is the signed JSON body (field order matters — the
// HMAC is over the exact serialized string; observed order:
// version, authority, issuedAt, expiresAt).
type browserCookiePayload struct {
	Version   int    `json:"version"`
	Authority string `json:"authority"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// mintBrowserCookie builds the v1 cookie locally from the persistent secret:
// "v1." + base64url(JSON payload) + "." + base64url(HMAC-SHA256(secret, body)).
// Mirrors browser-auth.ts encodeCookie; the server validates the signature,
// the authority binding, and the issuedAt→expiresAt window (≤ its maxAge).
func mintBrowserCookie(authority string, secret []byte, now time.Time) (string, string) {
	payload := browserCookiePayload{
		Version:   1,
		Authority: authority,
		IssuedAt:  now.UnixMilli(),
		ExpiresAt: now.Add(dshCookieMaxAge).UnixMilli(),
	}
	body, _ := json.Marshal(payload)
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(bodyB64))
	value := "v1." + bodyB64 + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return dshCookieName(authority), value
}

// ── dsh credentials store: targeted, shape-locked, read-only ────────────────
//
// The store also holds the user's API keys (refs/*). This scanner reads ONLY
// the records/<client-connection/browser-session> block: every other line is
// skipped without capturing its value. Any shape deviation fails closed —
// no general YAML parsing, no guessing.
//
// Pinned shape (live dump 2026-09-23, secrets redacted):
//
//	version: 1
//	refs:
//	  DEEPSEEK_API_KEY: <redacted>
//	records:
//	  client-connection/browser-session:
//	    kind: grant
//	    payload:
//	      version: 1
//	      secret: <43-char base64url>
func readBrowserSessionSecret(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("dshweb: no home directory for dsh credentials")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("dshweb: dsh credentials store unavailable: %w", err)
	}
	defer f.Close()

	// Scanner states: top-level keys → inside refs (foreign, skip) → inside
	// records → inside the target record → inside its payload block.
	const (
		stTop = iota
		stForeign
		stRecords
		stTarget
		stPayload
	)
	state := stTop
	fileVersion := 0
	kind := ""
	recVersion := 0
	secret := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		// Foreign sections (refs) are skipped wholesale until back at column 0.
		if state == stForeign {
			if indent == 0 {
				state = stTop
			} else {
				continue
			}
		}
		// Leaving a nested block re-enters the enclosing one.
		switch state {
		case stTarget:
			if indent <= 2 {
				state = stRecords
			}
		case stPayload:
			if indent <= 4 {
				state = stTarget
			}
		case stRecords:
			if indent == 0 {
				state = stTop
			}
		}

		key, val, ok := splitYAMLScalar(trimmed)
		if !ok {
			// Inside records/target we only ever expect plain scalars; at top
			// level anything unexpected fails closed rather than being guessed.
			if state == stTop || state == stRecords || state == stTarget || state == stPayload {
				return nil, fmt.Errorf("dshweb: credentials store: unparsable line %q", trimmed)
			}
			continue
		}

		switch state {
		case stTop:
			switch key {
			case "version":
				fileVersion = atoiStrict(val)
			case "refs":
				state = stForeign
			case "records":
				state = stRecords
			}
		case stRecords:
			if indent == 2 && strings.TrimSuffix(key, ":") == browserSessionRecordKey && val == "" {
				state = stTarget
			}
			// Foreign records at indent 2: their nested lines are skipped by
			// the indent guards above; nothing is captured.
		case stTarget:
			switch key {
			case "kind":
				kind = val
			case "payload":
				state = stPayload
			}
		case stPayload:
			switch key {
			case "version":
				recVersion = atoiStrict(val)
			case "secret":
				secret = strings.Trim(val, `"`)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("dshweb: credentials store read: %w", err)
	}
	if fileVersion != 1 {
		return nil, fmt.Errorf("dshweb: credentials store version %d unsupported (want 1)", fileVersion)
	}
	if kind != "grant" {
		return nil, fmt.Errorf("dshweb: browser-session record missing (kind=%q)", kind)
	}
	if recVersion != 1 {
		return nil, fmt.Errorf("dshweb: browser-session record version %d unsupported (want 1)", recVersion)
	}
	if secret == "" {
		return nil, fmt.Errorf("dshweb: browser-session secret missing")
	}
	raw, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("dshweb: browser-session secret malformed (want 32 bytes)")
	}
	return raw, nil
}

// splitYAMLScalar splits "key: value" (value empty for block keys). Returns
// ok=false for anything that is not a plain scalar line (flow maps/sequences,
// list items, anchors) — the scanner fails closed on those.
func splitYAMLScalar(trimmed string) (key, val string, ok bool) {
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") ||
		strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
		return "", "", false
	}
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(trimmed[:idx])
	val = ""
	if idx+1 < len(trimmed) {
		val = strings.TrimSpace(trimmed[idx+1:])
	}
	if strings.HasPrefix(val, "{") || strings.HasPrefix(val, "[") {
		return "", "", false
	}
	return key, val, true
}

func atoiStrict(s string) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return -1
	}
	return n
}
