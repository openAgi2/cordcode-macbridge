package codexremote

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type memJSONConn struct {
	mu       sync.Mutex
	inbox    []json.RawMessage
	outbox   []json.RawMessage
	readFail bool
}

func (c *memJSONConn) ReadJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.inbox) == 0 {
		c.readFail = true
		return errClosedSentinel{}
	}
	raw := c.inbox[0]
	c.inbox = c.inbox[1:]
	return json.Unmarshal(raw, v)
}

func (c *memJSONConn) WriteJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.outbox = append(c.outbox, raw)
	return nil
}

func TestAnswerDeviceKeyChallengeSignsConnectionProof(t *testing.T) {
	keys := newDeviceKeyStore()
	key, err := keys.Create()
	if err != nil {
		t.Fatal(err)
	}
	ctrl := "ctrl-token"
	exp := "2099-01-01T00:00:00Z"
	challenge := map[string]any{
		"type":                 "device_key_challenge",
		"clientId":             "client_probe",
		"targetOrigin":         controllerWSOrigin,
		"targetPath":           controllerWSPathFull,
		"tokenSha256Base64url": tokenSHA256Base64URL(ctrl),
		"tokenExpiresAt":       exp,
		"scopes":               []string{controllerWSScope},
		"accountUserId":        "acct",
		"audience":             "remote_control_controller_websocket",
		"nonce":                "n",
		"sessionId":            "sess",
	}
	raw, _ := json.Marshal(challenge)
	conn := &memJSONConn{inbox: []json.RawMessage{raw}}
	if err := answerDeviceKeyChallenge(conn, key, keys, "client_probe", ctrl, exp); err != nil {
		t.Fatal(err)
	}
	if len(conn.outbox) != 1 {
		t.Fatalf("proof writes = %d", len(conn.outbox))
	}
	var proof map[string]any
	if err := json.Unmarshal(conn.outbox[0], &proof); err != nil {
		t.Fatal(err)
	}
	if proof["type"] != "device_key_proof" {
		t.Fatalf("proof type = %v", proof["type"])
	}
	if proof["keyId"] != key.KeyID {
		t.Fatalf("keyId leaked mismatch")
	}
	if proof["algorithm"] != deviceKeyAlgorithm {
		t.Fatalf("algorithm = %v", proof["algorithm"])
	}
}

func TestAnswerDeviceKeyChallengeAcceptsUnixExpires(t *testing.T) {
	keys := newDeviceKeyStore()
	key, err := keys.Create()
	if err != nil {
		t.Fatal(err)
	}
	ctrl := "ctrl-token"
	iso := "2099-01-01T00:00:00Z"
	unix := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	challenge := map[string]any{
		"type":                 "device_key_challenge",
		"clientId":             "client_probe",
		"targetOrigin":         controllerWSOrigin,
		"targetPath":           controllerWSPathFull,
		"tokenSha256Base64url": tokenSHA256Base64URL(ctrl),
		"tokenExpiresAt":       unix,
		"scopes":               []string{controllerWSScope},
		"accountUserId":        "acct",
		"audience":             "remote_control_controller_websocket",
		"nonce":                "n",
		"sessionId":            "sess",
	}
	raw, _ := json.Marshal(challenge)
	conn := &memJSONConn{inbox: []json.RawMessage{raw}}
	if err := answerDeviceKeyChallenge(conn, key, keys, "client_probe", ctrl, iso); err != nil {
		t.Fatal(err)
	}
	var proof map[string]any
	if err := json.Unmarshal(conn.outbox[0], &proof); err != nil {
		t.Fatal(err)
	}
	if proof["type"] != "device_key_proof" {
		t.Fatalf("proof type = %v", proof["type"])
	}
}

func TestAnswerDeviceKeyChallengeRejectsMismatch(t *testing.T) {
	keys := newDeviceKeyStore()
	key, err := keys.Create()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"type": "device_key_challenge", "clientId": "other"})
	conn := &memJSONConn{inbox: []json.RawMessage{raw}}
	if err := answerDeviceKeyChallenge(conn, key, keys, "client_probe", "tok", "exp"); err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestControllerWSURLRewritesHTTPTestBase(t *testing.T) {
	prev := officialAPIBase
	officialAPIBase = "http://127.0.0.1:9"
	t.Cleanup(func() { officialAPIBase = prev })
	got := controllerWSURL()
	if got != "ws://127.0.0.1:9/codex/remote/control/client" {
		t.Fatalf("url = %s", got)
	}
}

func TestStreamHealthCheck(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	defer hostConn.Close()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_probe")
	defer stream.Close()
	cl := NewClient(stream, 1)
	defer cl.Close()

	if err := streamHealthCheck(cl, stream); err != nil {
		t.Fatalf("healthy stream check: %v", err)
	}

	stream.mu.Lock()
	stream.lastRecv = time.Now().Add(-2 * streamIdleLimit)
	stream.mu.Unlock()
	if err := streamHealthCheck(cl, stream); err == nil {
		t.Fatal("silent stream beyond the idle limit must fail health check; ping writes alone cannot detect it")
	}
}
