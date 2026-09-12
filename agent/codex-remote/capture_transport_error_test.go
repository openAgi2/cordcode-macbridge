package codexremote

// capture_transport_error_test.go is a one-shot, opt-in diagnostic harness
// (run with: go test ./agent/codex-remote -run TestCaptureTransportErrorStorm
// -tags stormcapture -v -timeout 120s). It opens a SECOND controller
// websocket connection using the same persisted pairing identity as the
// production runtime, observes RAW notifications BEFORE any codec
// suppression, and writes a redacted JSONL capture for an upstream bug
// report. It is gated behind the `stormcapture` build tag so it can never
// run as part of the normal suite.
//
// Redaction contract (bug report privacy):
//   - tokens, account ids, client/env ids, signatures, key material are
//     NEVER written; only counts and shapes.
//   - thread ids and turn ids are reduced to their first 8 hex chars.
//   - error messages are captured verbatim (they are relay-synthesized
//     transport text, not user content) but capped at 400 bytes; any
//     message containing "Bearer", "token", or "key" is replaced by
//     "[redacted-suspect-field]".
//   - the full raw params of the FIRST error frame are dumped to a separate
//     file with string VALUES over 24 chars truncated, so structure is
//     reviewable without leaking credentials.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type capturedFrame struct {
	Timestamp string `json:"ts"`
	Method    string `json:"method"`
	ParamsSHA string `json:"paramsSha256_16"`
	ParamsLen int    `json:"paramsBytes"`
	Error     string `json:"error,omitempty"` // redacted, capped
	WillRetry *bool  `json:"willRetry,omitempty"`
	ThreadID  string `json:"threadId8,omitempty"`
	TurnID    string `json:"turnId8,omitempty"`
}

func redactID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) <= 8 {
		sum := sha256.Sum256([]byte(id))
		return hex.EncodeToString(sum[:])[:8]
	}
	return id[:8]
}

func redactMessage(msg string) string {
	lower := strings.ToLower(msg)
	for _, suspect := range []string{"bearer", "token", "key", "secret", "authorization"} {
		if strings.Contains(lower, suspect) {
			return "[redacted-suspect-field]"
		}
	}
	if len(msg) > 400 {
		return msg[:400] + "…[truncated]"
	}
	return msg
}

func TestCaptureTransportErrorStorm(t *testing.T) {
	if os.Getenv("CAPTURE_OUT") == "" {
		t.Skip("opt-in diagnostic: set CAPTURE_OUT=<dir> to run")
	}
	outDir := os.Getenv("CAPTURE_OUT")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dataDir := os.Getenv("CAPTURE_DATA_DIR")
	if dataDir == "" {
		t.Fatal("CAPTURE_DATA_DIR must point at the runtime data dir (contains codex-remote-pairing.json)")
	}

	// Load persisted identity (read-only usage; the store file itself is not
	// modified — token refresh happens server-side and only rotates the
	// in-memory ctrl token of THIS process).
	p := newPairingController(nil)
	p.storePath = pairingStorePath(dataDir)
	rec, key, err := p.loadPersistedPairing()
	if err != nil {
		t.Fatalf("load persisted pairing: %v", err)
	}
	t.Logf("pairing identity loaded: clientId len=%d envId len=%d", len(rec.ClientID), len(rec.EnvID))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// 1. ChatGPT OAuth token via local app-server (same path as production).
	token, accountID, err := loadChatGPTAuth(ctx)
	if err != nil {
		t.Fatalf("chatgpt auth: %v", err)
	}
	t.Log("chatgpt auth ok (token not logged)")

	// 2. Build in-memory pairing state and refresh the control token.
	p.keys.Put(key)
	p.mu.Lock()
	p.state.clientID = rec.ClientID
	p.state.ctrlToken = rec.CtrlToken
	p.state.ctrlExp = rec.CtrlExp
	p.state.key = key
	p.state.env = &remoteEnv{EnvID: rec.EnvID, Online: false, ClientType: rec.ClientType}
	p.state.token = token
	p.state.accountID = accountID
	p.mu.Unlock()
	if err := p.refreshControlToken(ctx); err != nil {
		t.Fatalf("refresh control token: %v", err)
	}
	t.Log("control token refreshed (value not logged)")

	// 3. Resolve the desktop environment.
	env, err := p.lookupDesktopEnv(ctx, rec.EnvID)
	if err != nil {
		t.Fatalf("lookup desktop env: %v", err)
	}
	t.Logf("desktop env online=%v type=%s", env.Online, env.ClientType)

	// 4. Dial the controller websocket and answer the device-key challenge.
	streamID := randomStreamID()
	conn, err := dialControllerWS(ctx, p.state.token, p.state.accountID, p.state.ctrlToken, p.state.clientID)
	if err != nil {
		t.Fatalf("dial controller ws: %v", err)
	}
	if err := answerDeviceKeyChallenge(conn, key, p.keys, p.state.clientID, p.state.ctrlToken, p.state.ctrlExp); err != nil {
		_ = conn.Close()
		t.Fatalf("device key challenge: %v", err)
	}
	t.Log("websocket + device key challenge ok")

	// 5. Initialize the app-server session over this second stream.
	stream := NewStream(&wsFrameConn{conn: conn}, p.state.clientID, env.EnvID, streamID)
	cl := NewClient(stream, 1)
	defer cl.Close()
	raw, rpcErr, err := cl.RequestContext(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{"name": "codex_remote", "title": "CordCode Link storm capture", "version": "0"},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil || rpcErr != nil {
		t.Fatalf("initialize: err=%v rpcErr=%v", err, rpcErr)
	}
	var initResult struct {
		UserAgent string `json:"userAgent"`
	}
	_ = json.Unmarshal(raw, &initResult)
	t.Logf("initialize ok; server userAgent=%q", initResult.UserAgent)
	if err := cl.Notify("initialized", map[string]any{}); err != nil {
		t.Fatalf("initialized notify: %v", err)
	}

	// 6. Observe RAW notifications (pre-codec, pre-suppression) for a bounded
	// window: stop after 3 error frames or 30 seconds, whichever first.
	framesPath := filepath.Join(outDir, "storm-frames.jsonl")
	framesFile, err := os.OpenFile(framesPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer framesFile.Close()

	var total, errors atomic.Int64
	var firstErrorParams atomic.Value // raw params of first error frame
	deadline := time.Now().Add(30 * time.Second)
	notifications := cl.Notifications()

	for time.Now().Before(deadline) {
		select {
		case n, ok := <-notifications:
			if !ok {
				t.Fatal("notification channel closed (stream died)")
			}
			total.Add(1)
			frame := capturedFrame{
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				Method:    n.Method,
				ParamsSHA: func() string { s := sha256.Sum256(n.Params); return hex.EncodeToString(s[:])[:16] }(),
				ParamsLen: len(n.Params),
			}
			if n.Method == "error" {
				errors.Add(1)
				var params struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
					WillRetry bool   `json:"willRetry"`
					ThreadID  string `json:"threadId"`
					TurnID    string `json:"turnId"`
				}
				_ = json.Unmarshal(n.Params, &params)
				frame.Error = redactMessage(params.Error.Message)
				wr := params.WillRetry
				frame.WillRetry = &wr
				frame.ThreadID = redactID(params.ThreadID)
				frame.TurnID = redactID(params.TurnID)
				if firstErrorParams.Load() == nil {
					firstErrorParams.Store([]byte(n.Params))
				}
			}
			line, _ := json.Marshal(frame)
			if _, err := framesFile.Write(append(line, '\n')); err != nil {
				t.Fatal(err)
			}
			if errors.Load() >= 3 {
				t.Logf("captured %d error frames; stopping early", errors.Load())
				goto done
			}
		case <-time.After(2 * time.Second):
			// keep waiting until overall deadline
		}
	}
done:
	t.Logf("capture complete: total notifications=%d error frames=%d", total.Load(), errors.Load())

	// 7. Dump the first error frame's structure with long values truncated.
	if raw := firstErrorParams.Load(); raw != nil {
		var v any
		if json.Unmarshal(raw.([]byte), &v) == nil {
			truncateLongStrings(v, 24)
			if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
				path := filepath.Join(outDir, "storm-first-error-params-redacted.json")
				if err := os.WriteFile(path, pretty, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Logf("first error params (redacted) written to %s", path)
			}
		}
	}
}

// truncateLongStrings shortens every string VALUE deeper than max runes in
// place, preserving object keys and structure.
func truncateLongStrings(v any, max int) {
	switch x := v.(type) {
	case map[string]any:
		for k, item := range x {
			if s, ok := item.(string); ok && len(s) > max {
				x[k] = s[:max] + "…[" + fmt.Sprint(len(s)) + "B truncated]"
			} else {
				truncateLongStrings(item, max)
			}
		}
	case []any:
		for i, item := range x {
			if s, ok := item.(string); ok && len(s) > max {
				x[i] = s[:max] + "…[" + fmt.Sprint(len(s)) + "B truncated]"
			} else {
				truncateLongStrings(item, max)
			}
		}
	}
}
