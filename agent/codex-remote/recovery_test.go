package codexremote

// P1 401-recovery tests (docs/2026-10-12-codex-remote-probe-economy-p1-
// plan.md §3 D3 / S-3): a rejected account auth on the refresh endpoints must
// first drop the cache and re-probe once (official recovery-first,
// auth.rs:177-214); only a second rejection revokes. Non-auth refresh
// failures keep the caller fallback contract (r1 F-2: try the saved ctrl
// token). Explicit pairing flows must never touch the cache (R-5).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// recoveryServer counts refresh attempts and lets a callback pick the
// status per attempt (0 = OK). The environments route serves `online`.
func recoveryServer(t *testing.T, key *deviceKey, online bool, refreshStatus func(attempt int) int) (*httptest.Server, *gateCounts) {
	t.Helper()
	counts := &gateCounts{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/codex/remote/control/client/refresh/start":
			counts.mu.Lock()
			counts.refreshStart++
			attempt := counts.refreshStart
			counts.mu.Unlock()
			if status := refreshStatus(attempt); status != 0 && status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
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

// newRecoveryAgent serves tokenA on the first probe and tokenB afterwards —
// modeling a stale cached auth replaced by a fresh probe.
func newRecoveryAgent(t *testing.T, key *deviceKey) (*Agent, *int) {
	t.Helper()
	agent := newPersistedAgent(t, key) // persisted ctrl expired → Required
	calls := 0
	tokenA := cacheTestJWT(t, "acct", time.Now().Add(2*time.Hour), true)
	tokenB := cacheTestJWT(t, "acct", time.Now().Add(2*time.Hour), true)
	agent.pairing.authTokenFn = func(context.Context) (string, string, error) {
		calls++
		if calls == 1 {
			return tokenA, "acct", nil
		}
		return tokenB, "acct", nil
	}
	return agent, &calls
}

func TestCtrlRefreshAuthRejectedOnceRecovers(t *testing.T) {
	// First refresh rejects the stale tokenA; recovery re-probes (tokenB)
	// and the retry succeeds — no revocation, restore completes.
	for _, tc := range []struct {
		name   string
		status int
	}{{"401", http.StatusUnauthorized}, {"403", http.StatusForbidden}} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := newDeviceKeyStore().Create()
			if err != nil {
				t.Fatal(err)
			}
			srv, counts := recoveryServer(t, key, true, func(attempt int) int {
				if attempt == 1 {
					return tc.status
				}
				return 0
			})
			t.Cleanup(srv.Close)
			prev := officialAPIBase
			officialAPIBase = srv.URL
			t.Cleanup(func() { officialAPIBase = prev })

			agent, calls := newRecoveryAgent(t, key)
			agent.pairing.http = srv.Client()
			stubBind(t, agent)

			snap, err := agent.StartRemoteControl(context.Background(), "", "")
			if err != nil {
				t.Fatalf("single %d must recover, not fail: %v", tc.status, err)
			}
			if snap.Phase != PairPhaseReady {
				t.Fatalf("phase after recovery: %+v", snap)
			}
			if *calls != 2 {
				t.Fatalf("recovery must re-probe exactly once, got %d calls", *calls)
			}
			if refreshes, _ := counts.snapshot(); refreshes != 2 {
				t.Fatalf("expected initial + retried refresh, got %d", refreshes)
			}
		})
	}
}

func TestCtrlRefreshAuthRejectedTwiceRevokes(t *testing.T) {
	// After recovery also gets rejected the pairing is revoked — the
	// pre-existing terminal semantics (TestRestoreRevokedInvalidatesEveryIdentityLayer)
	// must survive the recovery insertion.
	for _, tc := range []struct {
		name   string
		status int
	}{{"401", http.StatusUnauthorized}, {"403", http.StatusForbidden}} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := newDeviceKeyStore().Create()
			if err != nil {
				t.Fatal(err)
			}
			srv, _ := recoveryServer(t, key, true, func(int) int { return tc.status })
			t.Cleanup(srv.Close)
			prev := officialAPIBase
			officialAPIBase = srv.URL
			t.Cleanup(func() { officialAPIBase = prev })

			agent, calls := newRecoveryAgent(t, key)
			agent.pairing.http = srv.Client()
			storePath := agent.pairing.storePath

			snap, err := agent.StartRemoteControl(context.Background(), "", "")
			if err == nil {
				t.Fatalf("persistent %d must revoke", tc.status)
			}
			if snap.Phase != PairPhaseFailed {
				t.Fatalf("phase after double rejection: %+v", snap)
			}
			if *calls != 2 {
				t.Fatalf("revocation must consume initial + recovery probes, got %d", *calls)
			}
			if _, statErr := os.Stat(storePath); statErr == nil {
				t.Fatal("revocation must delete the pairing store")
			}
		})
	}
}

func TestRestoreRefreshTransientFailureKeepsSavedTokenFallback(t *testing.T) {
	// r1 F-2: a non-auth refresh failure (5xx) must not fail the round —
	// restore continues with the saved ctrl token and still binds.
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv, counts := recoveryServer(t, key, true, func(int) int { return http.StatusServiceUnavailable })
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent, calls := newRecoveryAgent(t, key)
	agent.pairing.http = srv.Client()
	stubBind(t, agent)

	if _, err := agent.StartRemoteControl(context.Background(), "", ""); err != nil {
		t.Fatalf("5xx refresh must fall back to the saved ctrl token: %v", err)
	}
	refreshes, envCalls := counts.snapshot()
	if refreshes < 1 {
		t.Fatal("refresh must have been attempted")
	}
	if envCalls < 1 {
		t.Fatal("env lookup must still run after the refresh fallback")
	}
	if *calls != 1 {
		t.Fatalf("non-auth failure must not trigger a recovery probe, got %d calls", *calls)
	}
}

func TestExplicitPairingFlowNeverTouchesAuthCache(t *testing.T) {
	// R-5: the explicit pairing flow takes the token from its caller and
	// must neither read nor populate the auth cache.
	var gotPairCode bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/codex/remote/control/client/enroll/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id": "client_probe",
				"device_key_challenge": map[string]any{
					"account_user_id": "acct", "audience": "remote_control_client_enrollment",
					"challenge_expires_at": "2099-01-01T00:00:00Z", "challenge_id": "ch",
					"challenge_token": "tok", "client_id": "client_probe",
					"nonce": "n", "target_origin": "https://chatgpt.com",
					"target_path": "/backend-api/codex/remote/control/client/enroll/finish",
					"purpose":     "remote_control_client_enrollment",
				},
			})
		case r.URL.Path == "/codex/remote/control/client/enroll/finish":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"remote_control_token": "ctrl", "expires_at": "2099-01-01T00:00:00Z",
				"scopes": []string{"remote_control_controller_websocket"},
			})
		case r.URL.Path == "/wham/remote/control/client/pair":
			gotPairCode = true
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{}`))
		case strings.Contains(r.URL.Path, "/environments"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []any{map[string]any{
					"env_id": "env1", "online": true, "client_type": "CODEX_DESKTOP_APP", "os": "Mac OS",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	dir := t.TempDir()
	agent := New(map[string]any{"data_dir": dir, "skip_restore": true})
	agent.pairing.http = srv.Client()
	agent.pairing.stepUpToken = "step-up"
	stubBind(t, agent)
	if _, err := agent.StartRemoteControl(context.Background(), "account-jwt", "acct"); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.SubmitRemoteControlCode(context.Background(), "ABCD-EFGH"); err != nil {
		t.Fatal(err)
	}
	if !gotPairCode {
		t.Fatal("official pair was not called")
	}
	agent.pairing.mu.Lock()
	cached := agent.pairing.auth
	agent.pairing.mu.Unlock()
	if cached != nil {
		t.Fatal("explicit pairing flow must not populate the auth cache (R-5)")
	}
}
