package codexremote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPairingStartAndSubmitCode(t *testing.T) {
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
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), "manual_pairing_code") {
				t.Errorf("pair body missing code field")
			}
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

	agent := New(nil)
	agent.pairing.http = srv.Client()
	agent.pairing.stepUpToken = "step-up"
	agent.pairing.bindEnv = func(ctx context.Context, env *remoteEnv) error {
		clientConn, hostConn := LoopbackPair()
		startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
			if method == "initialize" {
				return map[string]any{"userAgent": "codex"}, nil
			}
			return map[string]any{}, nil
		})
		if err := agent.pairing.activateStream(ctx, clientConn, "client_probe", env.EnvID); err != nil {
			return err
		}
		t.Cleanup(func() {
			agent.mu.Lock()
			cl := agent.client
			agent.mu.Unlock()
			if cl != nil {
				_ = cl.Close()
			}
		})
		return nil
	}
	snap, err := agent.StartRemoteControl(context.Background(), "account-jwt", "acct")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Phase != PairPhaseAwaitingCode {
		t.Fatalf("phase = %s message=%s", snap.Phase, snap.Message)
	}
	snap, err = agent.SubmitRemoteControlCode(context.Background(), "ABCD-EFGH")
	if err != nil {
		t.Fatal(err)
	}
	if !gotPairCode {
		t.Fatal("official pair was not called")
	}
	if snap.Phase != PairPhaseReady || !snap.Online {
		t.Fatalf("snap = %+v", snap)
	}
	ok, detail := agent.InstanceStatus()
	if !ok {
		t.Fatalf("status %q", detail)
	}
}

func TestPairingRejectsEmptyCode(t *testing.T) {
	agent := New(nil)
	if _, err := agent.SubmitRemoteControlCode(context.Background(), "  "); err == nil {
		t.Fatal("expected empty code error")
	}
}

func TestSubmitCodeWhileAuthorizingDoesNotMarkFailed(t *testing.T) {
	agent := New(nil)
	agent.pairing.mu.Lock()
	agent.pairing.state.phase = PairPhaseAuthorizing
	agent.pairing.state.message = "请在打开的浏览器完成授权，然后回到这里输入电脑配对码"
	agent.pairing.mu.Unlock()
	snap, err := agent.SubmitRemoteControlCode(context.Background(), "ABCD-EFGH")
	if err == nil {
		t.Fatal("expected not-ready error")
	}
	if snap.Phase != PairPhaseAuthorizing {
		t.Fatalf("premature submit must not mark failed, phase=%s", snap.Phase)
	}
	if snap.Phase == PairPhaseFailed {
		t.Fatal("authorizing pairing was marked failed")
	}
}

func TestInstanceStatusUnpaired(t *testing.T) {
	ok, detail := New(nil).InstanceStatus()
	if ok {
		t.Fatal("unpaired agent must not be available")
	}
	if detail == "" {
		t.Fatal("need a user-facing reason")
	}
}

func TestPersistedPairingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keys := newDeviceKeyStore()
	key, err := keys.Create()
	if err != nil {
		t.Fatal(err)
	}
	agent := New(map[string]any{"data_dir": dir})
	agent.pairing.keys = keys
	agent.pairing.mu.Lock()
	agent.pairing.state.clientID = "client_probe"
	agent.pairing.state.ctrlToken = "ctrl"
	agent.pairing.state.ctrlExp = "2099-01-01T00:00:00Z"
	agent.pairing.state.key = key
	agent.pairing.state.env = &remoteEnv{EnvID: "env1", Online: true, ClientType: "CODEX_DESKTOP_APP"}
	agent.pairing.mu.Unlock()
	if err := agent.pairing.savePersistedPairing(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pairingStorePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var rec persistedPairing
	if json.Unmarshal(raw, &rec) != nil || rec.ClientID != "client_probe" || rec.PrivateKeyPKCS8Base64 == "" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.CtrlToken != "ctrl" {
		t.Fatal("token missing from store")
	}
}

func TestSubmitCodeWritesPairingStore(t *testing.T) {
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
	raw, err := os.ReadFile(pairingStorePath(dir))
	if err != nil {
		t.Fatalf("pairing store missing after successful pair: %v", err)
	}
	var rec persistedPairing
	if json.Unmarshal(raw, &rec) != nil || rec.ClientID != "client_probe" || rec.EnvID != "env1" || rec.CtrlToken != "ctrl" || rec.PrivateKeyPKCS8Base64 == "" {
		t.Fatalf("record = %+v", rec)
	}
}

func newPersistedAgent(t *testing.T, key *deviceKey) *Agent {
	t.Helper()
	dir := t.TempDir()
	agent := New(map[string]any{"data_dir": dir, "skip_restore": true})
	agent.pairing.keys.Put(key)
	agent.pairing.mu.Lock()
	agent.pairing.state.clientID = "client_probe"
	agent.pairing.state.ctrlToken = "ctrl-old"
	agent.pairing.state.ctrlExp = "2000-01-01T00:00:00Z"
	agent.pairing.state.key = key
	agent.pairing.state.env = &remoteEnv{EnvID: "env1", ClientType: "CODEX_DESKTOP_APP"}
	agent.pairing.mu.Unlock()
	if err := agent.pairing.savePersistedPairing(); err != nil {
		t.Fatal(err)
	}
	agent.pairing.authTokenFn = func(context.Context) (string, string, error) {
		return "account-jwt", "acct", nil
	}
	return agent
}

func restoreHTTPServer(t *testing.T, key *deviceKey, online bool, refreshStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/codex/remote/control/client/enroll/start":
			t.Errorf("restore must not enroll a new controller")
			http.NotFound(w, r)
		case r.URL.Path == "/codex/remote/control/client/refresh/start":
			if refreshStatus != 0 && refreshStatus != http.StatusOK {
				w.WriteHeader(refreshStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id": "client_probe",
				"device_key_challenge": map[string]any{
					"account_user_id": "acct", "audience": "remote_control_client_enrollment",
					"challenge_expires_at": "2099-01-01T00:00:00Z", "challenge_id": "ch",
					"challenge_token": "tok", "client_id": "client_probe",
					"nonce": "n", "target_origin": "https://chatgpt.com",
					"target_path": "/backend-api/codex/remote/control/client/refresh/finish",
					"purpose":     "remote_control_client_enrollment",
					"device_identity_hash": key.identityHash(),
				},
			})
		case r.URL.Path == "/codex/remote/control/client/refresh/finish":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"remote_control_token": "ctrl-new", "expires_at": "2099-01-01T00:00:00Z",
				"scopes": []string{"remote_control_controller_websocket"},
			})
		case strings.Contains(r.URL.Path, "/environments"):
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
}

func stubBind(t *testing.T, agent *Agent) {
	t.Helper()
	agent.pairing.bindEnv = func(ctx context.Context, env *remoteEnv) error {
		clientConn, hostConn := LoopbackPair()
		startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
			if method == "initialize" {
				return map[string]any{"userAgent": "codex"}, nil
			}
			return map[string]any{}, nil
		})
		if err := agent.pairing.activateStream(ctx, clientConn, "client_probe", env.EnvID); err != nil {
			return err
		}
		t.Cleanup(func() {
			agent.mu.Lock()
			cl := agent.client
			agent.mu.Unlock()
			if cl != nil {
				_ = cl.Close()
			}
		})
		return nil
	}
}

func TestRestoreRefreshesTokenThenBinds(t *testing.T) {
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv := restoreHTTPServer(t, key, true, http.StatusOK)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent := newPersistedAgent(t, key)
	agent.pairing.http = srv.Client()
	stubBind(t, agent)

	snap, err := agent.StartRemoteControl(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Phase != PairPhaseReady {
		t.Fatalf("phase=%s message=%s", snap.Phase, snap.Message)
	}
	raw, err := os.ReadFile(agent.pairing.storePath)
	if err != nil {
		t.Fatal(err)
	}
	var rec persistedPairing
	if json.Unmarshal(raw, &rec) != nil || rec.CtrlToken != "ctrl-new" {
		t.Fatalf("refreshed token not saved: %+v", rec)
	}
	ok, _ := agent.InstanceStatus()
	if !ok {
		t.Fatal("restored agent must be available")
	}
}

func TestRestoreOfflineKeepsStore(t *testing.T) {
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv := restoreHTTPServer(t, key, false, http.StatusOK)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent := newPersistedAgent(t, key)
	agent.pairing.http = srv.Client()
	snap, err := agent.StartRemoteControl(context.Background(), "", "")
	if err == nil {
		t.Fatal("expected offline wait")
	}
	if snap.Phase != PairPhaseOffline {
		t.Fatalf("phase=%s message=%s", snap.Phase, snap.Message)
	}
	if _, statErr := os.Stat(agent.pairing.storePath); statErr != nil {
		t.Fatal("offline restore must keep pairing store")
	}
	ok, detail := agent.InstanceStatus()
	if ok {
		t.Fatal("offline agent must not be available")
	}
	if !strings.Contains(detail, "ChatGPT Desktop") {
		t.Fatalf("detail=%q", detail)
	}
}

func TestRestoreRevokedDeletesStore(t *testing.T) {
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv := restoreHTTPServer(t, key, true, http.StatusForbidden)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent := newPersistedAgent(t, key)
	agent.pairing.http = srv.Client()
	snap, err := agent.StartRemoteControl(context.Background(), "", "")
	if err == nil {
		t.Fatal("expected revoked error")
	}
	if snap.Phase != PairPhaseFailed {
		t.Fatalf("phase=%s", snap.Phase)
	}
	if _, statErr := os.Stat(agent.pairing.storePath); !os.IsNotExist(statErr) {
		t.Fatal("revoked pairing must delete store")
	}
}
