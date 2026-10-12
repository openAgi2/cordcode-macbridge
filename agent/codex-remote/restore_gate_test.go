package codexremote

// P1 restore-gating tests (docs/2026-10-12-codex-remote-probe-economy-p1-
// plan.md §3 D1 / S-2): the four states from r1 F-1 — cold start with a
// still-fresh persisted ctrl token must still restore (1 probe, 0 refresh,
// env lookup runs); a filled cache with fresh ctrl must probe 0; the
// Proactive window must refresh; and an auth-rejected env list must
// invalidate the cache so the next round re-probes.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type gateCounts struct {
	mu           sync.Mutex
	refreshStart int
	envCalls     int
}

func (c *gateCounts) snapshot() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshStart, c.envCalls
}

// gateRestoreServer is restoreHTTPServer with request counters and an
// optional forced status on the environments route.
func gateRestoreServer(t *testing.T, key *deviceKey, online bool, envStatus int) (*httptest.Server, *gateCounts) {
	t.Helper()
	counts := &gateCounts{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/codex/remote/control/client/enroll/start":
			t.Errorf("restore must not enroll a new controller")
			http.NotFound(w, r)
		case r.URL.Path == "/codex/remote/control/client/refresh/start":
			counts.mu.Lock()
			counts.refreshStart++
			counts.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id": "client_probe",
				"device_key_challenge": map[string]any{
					"account_user_id": "acct", "audience": "remote_control_client_enrollment",
					"challenge_expires_at": "2099-01-01T00:00:00Z", "challenge_id": "ch",
					"challenge_token": "tok", "client_id": "client_probe",
					"nonce": "n", "target_origin": "https://chatgpt.com",
					"target_path":          "/backend-api/codex/remote/control/client/refresh/finish",
					"purpose":              "remote_control_client_enrollment",
					"device_identity_hash": key.identityHash(),
				},
			})
		case r.URL.Path == "/codex/remote/control/client/refresh/finish":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"remote_control_token": "ctrl-new", "expires_at": "2099-01-01T00:00:00Z",
				"scopes": []string{"remote_control_controller_websocket"},
			})
		case strings.Contains(r.URL.Path, "/environments"):
			counts.mu.Lock()
			counts.envCalls++
			counts.mu.Unlock()
			if envStatus != 0 {
				w.WriteHeader(envStatus)
				return
			}
			items := []any{}
			if online {
				items = append(items, map[string]any{
					"env_id": "env1", "online": true, "client_type": "CODEX_DESKTOP_APP", "os": "Mac OS",
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, counts
}

// newGatedAgent persists a pairing with the given ctrl expiry and counts
// auth probes through the existing authTokenFn seam.
func newGatedAgent(t *testing.T, key *deviceKey, ctrlExp string) (*Agent, *int) {
	t.Helper()
	agent := newPersistedAgent(t, key)
	agent.pairing.mu.Lock()
	agent.pairing.state.ctrlExp = ctrlExp
	agent.pairing.mu.Unlock()
	if err := agent.pairing.savePersistedPairing(); err != nil {
		t.Fatal(err)
	}
	calls := 0
	token := cacheTestJWT(t, "acct", time.Now().Add(2*time.Hour), true)
	agent.pairing.authTokenFn = func(context.Context) (string, string, error) {
		calls++
		return token, "acct", nil
	}
	return agent, &calls
}

func primeAuthCache(t *testing.T, agent *Agent) {
	t.Helper()
	if _, _, err := agent.pairing.chatGPTAuthCached(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreColdStartCtrlFreshProbesOnceSkipsRefresh(t *testing.T) {
	// r1 F-1: a fresh persisted ctrl token must not gate the auth fetch —
	// cold start probes exactly once and still restores when Desktop opens.
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv, counts := gateRestoreServer(t, key, true, 0)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent, calls := newGatedAgent(t, key, time.Now().Add(9*time.Minute).Format(time.RFC3339))
	agent.pairing.http = srv.Client()
	stubBind(t, agent)

	if _, err := agent.StartRemoteControl(context.Background(), "", ""); err != nil {
		t.Fatalf("cold start with fresh ctrl must restore: %v", err)
	}
	refreshes, envCalls := counts.snapshot()
	if *calls != 1 {
		t.Fatalf("cold start must probe exactly once (miss), got %d", *calls)
	}
	if refreshes != 0 {
		t.Fatalf("fresh ctrl token must skip refresh, got %d refresh calls", refreshes)
	}
	if envCalls < 1 {
		t.Fatal("env lookup must still run when refresh is gated")
	}
}

func TestRestoreCacheFilledCtrlFreshProbesZero(t *testing.T) {
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv, counts := gateRestoreServer(t, key, true, 0)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent, calls := newGatedAgent(t, key, time.Now().Add(9*time.Minute).Format(time.RFC3339))
	agent.pairing.http = srv.Client()
	stubBind(t, agent)
	primeAuthCache(t, agent)

	if _, err := agent.StartRemoteControl(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	refreshes, envCalls := counts.snapshot()
	if *calls != 1 {
		t.Fatalf("primed cache must serve restore without a new probe, got %d calls", *calls)
	}
	if refreshes != 0 {
		t.Fatalf("fresh ctrl token must skip refresh, got %d", refreshes)
	}
	if envCalls < 1 {
		t.Fatal("env lookup must still run")
	}
}

func TestRestoreProactiveWindowStillRefreshes(t *testing.T) {
	// Inside the final ctrlTokenSkewSecs window the gate must let the
	// refresh through (official Proactive level).
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv, counts := gateRestoreServer(t, key, true, 0)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent, _ := newGatedAgent(t, key, time.Now().Add(2*time.Minute).Format(time.RFC3339))
	agent.pairing.http = srv.Client()
	stubBind(t, agent)

	if _, err := agent.StartRemoteControl(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	refreshes, _ := counts.snapshot()
	if refreshes < 1 {
		t.Fatal("ctrl token inside the skew window must be refreshed")
	}
}

func TestRestoreEnvAuthRejectedInvalidatesCache(t *testing.T) {
	// A 401 on the env list means the account auth is stale — the cache must
	// be dropped so the next round genuinely re-probes (P1 §3 D2).
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv, counts := gateRestoreServer(t, key, false, http.StatusUnauthorized)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent, calls := newGatedAgent(t, key, time.Now().Add(9*time.Minute).Format(time.RFC3339))
	agent.pairing.http = srv.Client()
	primeAuthCache(t, agent)

	if _, err := agent.StartRemoteControl(context.Background(), "", ""); err == nil {
		t.Fatal("env 401 must fail the restore round")
	}
	if *calls != 1 {
		t.Fatalf("first round must serve auth from cache, got %d calls", *calls)
	}
	agent.pairing.mu.Lock()
	cached := agent.pairing.auth
	agent.pairing.mu.Unlock()
	if cached != nil {
		t.Fatal("env 401 must invalidate the auth cache")
	}
	// The next round must re-probe (fresh miss) instead of serving the
	// dead token again.
	if _, err := agent.StartRemoteControl(context.Background(), "", ""); err == nil {
		t.Fatal("second round against 401 env must still fail")
	}
	if *calls != 2 {
		t.Fatalf("round after invalidation must re-probe, got %d calls", *calls)
	}
	if _, envCalls := counts.snapshot(); envCalls < 2 {
		t.Fatalf("both rounds must attempt the env lookup, got %d", envCalls)
	}
}
