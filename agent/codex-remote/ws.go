package codexremote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	controllerWSPath     = "/codex/remote/control/client"
	controllerWSOrigin   = "https://chatgpt.com"
	controllerWSPathFull = "/backend-api/codex/remote/control/client"
	controllerWSScope    = "remote_control_controller_websocket"
)

type jsonConn interface {
	ReadJSON(v any) error
	WriteJSON(v any) error
}

type wsFrameConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *wsFrameConn) Write(env Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(env)
}

func (c *wsFrameConn) Read() (Envelope, error) {
	var env Envelope
	if err := c.conn.ReadJSON(&env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func (c *wsFrameConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

func controllerWSURL() string {
	base := strings.TrimRight(officialAPIBase, "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://") + controllerWSPath
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://") + controllerWSPath
	default:
		return base + controllerWSPath
	}
}

func randomStreamID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func dialControllerWS(ctx context.Context, token, accountID, ctrlToken, clientID string) (*websocket.Conn, error) {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	header.Set("ChatGPT-Account-Id", accountID)
	header.Set("originator", "Codex Desktop")
	header.Set("Origin", controllerWSOrigin)
	header.Set("x-codex-client-id", clientID)
	header.Set("x-codex-protocol-version", ProtocolVersion)
	header.Set("x-codex-client-session-token", "Bearer "+ctrlToken)
	dialer := websocket.Dialer{
		HandshakeTimeout:  15 * time.Second,
		EnableCompression: false,
	}
	conn, resp, err := dialer.DialContext(ctx, controllerWSURL(), header)
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	if err != nil {
		slog.Warn("codex-remote pairing websocket handshake failed", "status", status)
		return nil, fmt.Errorf("controller websocket handshake failed")
	}
	conn.SetReadLimit(int64(WireEnvelopeMaxBytes))
	return conn, nil
}

func tokenSHA256Base64URL(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type deviceKeyChallenge struct {
	Type           string          `json:"type"`
	ClientID       string          `json:"clientId"`
	TargetOrigin   string          `json:"targetOrigin"`
	TargetPath     string          `json:"targetPath"`
	TokenSHA256    string          `json:"tokenSha256Base64url"`
	TokenExpiresAt json.RawMessage `json:"tokenExpiresAt"`
	Scopes         []string        `json:"scopes"`
	AccountUserID  string          `json:"accountUserId"`
	Audience       string          `json:"audience"`
	Nonce          string          `json:"nonce"`
	SessionID      string          `json:"sessionId"`
}

func decodeExpiresUnix(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.Unix(), true
	}
	return 0, false
}

func parseExpiresUnix(s string) (int64, bool) {
	s = strings.TrimSpace(strings.Trim(s, `"`))
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.Unix(), true
	}
	return 0, false
}

func expiresUnixEqual(challengeExp json.RawMessage, ctrlExp string) bool {
	left, okLeft := decodeExpiresUnix(challengeExp)
	right, okRight := parseExpiresUnix(ctrlExp)
	return okLeft && okRight && left == right
}

func answerDeviceKeyChallenge(conn jsonConn, key *deviceKey, keys *deviceKeyStore, clientID, ctrlToken, ctrlExp string) error {
	if key == nil || keys == nil {
		return fmt.Errorf("missing device key")
	}
	var challenge deviceKeyChallenge
	if err := conn.ReadJSON(&challenge); err != nil {
		slog.Warn("codex-remote pairing challenge unreadable")
		return fmt.Errorf("device key challenge unreadable")
	}
	typeOK := challenge.Type == "device_key_challenge"
	clientOK := challenge.ClientID == clientID
	originOK := challenge.TargetOrigin == controllerWSOrigin
	pathOK := challenge.TargetPath == controllerWSPathFull
	hashOK := challenge.TokenSHA256 == tokenSHA256Base64URL(ctrlToken)
	expOK := expiresUnixEqual(challenge.TokenExpiresAt, ctrlExp)
	scopeOK := len(challenge.Scopes) == 1 && challenge.Scopes[0] == controllerWSScope
	if !typeOK || !clientOK || !originOK || !pathOK || !hashOK || !expOK || !scopeOK {
		slog.Warn("codex-remote pairing challenge mismatch",
			"type_ok", typeOK, "client_ok", clientOK, "origin_ok", originOK,
			"path_ok", pathOK, "hash_ok", hashOK, "exp_ok", expOK, "scope_ok", scopeOK)
		return fmt.Errorf("device key challenge mismatch")
	}
	payload := map[string]any{
		"accountUserId":        challenge.AccountUserID,
		"audience":             challenge.Audience,
		"clientId":             challenge.ClientID,
		"nonce":                challenge.Nonce,
		"scopes":               challenge.Scopes,
		"sessionId":            challenge.SessionID,
		"targetOrigin":         challenge.TargetOrigin,
		"targetPath":           challenge.TargetPath,
		"tokenExpiresAt":       challenge.TokenExpiresAt,
		"tokenSha256Base64url": challenge.TokenSHA256,
		"type":                 "remoteControlClientConnection",
	}
	signedPayload, signature, err := keys.signEnrollment(key, payload)
	if err != nil {
		return err
	}
	proof := map[string]any{
		"type":                "device_key_proof",
		"keyId":               key.KeyID,
		"signatureDerBase64":  signature,
		"signedPayloadBase64": signedPayload,
		"algorithm":           key.Algorithm,
	}
	if err := conn.WriteJSON(proof); err != nil {
		return fmt.Errorf("device key proof write failed")
	}
	return nil
}

func (p *PairingController) bindLive(ctx context.Context, env *remoteEnv) error {
	p.mu.Lock()
	token := p.state.token
	accountID := p.state.accountID
	ctrlToken := p.state.ctrlToken
	ctrlExp := p.state.ctrlExp
	clientID := p.state.clientID
	key := p.state.key
	p.mu.Unlock()
	if token == "" || accountID == "" || ctrlToken == "" || clientID == "" || env == nil || env.EnvID == "" {
		return fmt.Errorf("pairing state incomplete")
	}
	// A stream id is the identity of one logical app-server connection, matching
	// upstream ClientTracker's (client_id, stream_id) key. Every new Client/epoch
	// gets a fresh id so late responses from an older connection cannot correlate
	// with request ids restarted at 1.
	streamID := randomStreamID()
	p.mu.Lock()
	p.state.streamID = streamID
	p.mu.Unlock()
	conn, err := dialControllerWS(ctx, token, accountID, ctrlToken, clientID)
	if err != nil {
		return err
	}
	if err := answerDeviceKeyChallenge(conn, key, p.keys, clientID, ctrlToken, ctrlExp); err != nil {
		_ = conn.Close()
		return err
	}
	if err := p.activateStream(ctx, &wsFrameConn{conn: conn}, clientID, env.EnvID, streamID); err != nil {
		slog.Warn("codex-remote pairing initialize failed")
		return err
	}
	slog.Info("codex-remote pairing", "phase", "stream_bound")
	a := p.agent
	a.mu.Lock()
	a.paired = true
	a.mu.Unlock()
	p.mu.Lock()
	if env != nil {
		p.state.env = env
	}
	p.mu.Unlock()
	p.persistPairingOrLog()
	return nil
}

func (p *PairingController) activateStream(ctx context.Context, conn FrameConn, clientID, envID, streamID string) error {
	stream := NewStream(conn, clientID, envID, streamID)
	p.agent.mu.Lock()
	p.agent.connEpoch++
	epoch := p.agent.connEpoch
	p.agent.mu.Unlock()
	cl := NewClient(stream, epoch)
	p.agent.BindClient(cl)
	raw, rpcErr, err := cl.RequestContext(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "codex_remote",
			"title":   "CordCode Link",
			"version": "0",
		},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil {
		_ = cl.Close()
		return err
	}
	if rpcErr != nil {
		_ = cl.Close()
		return rpcErr
	}
	// The initialize result carries the app-server's userAgent
	// ("{originator}/{workspace-version} (...)"); it is the version fact the
	// resume initialTurnsPage allowlist gates on. BindClient already cleared
	// the previous epoch's version, so attaches racing ahead of this point
	// pre-select the baseline until the new version is announced.
	var initResult struct {
		UserAgent string `json:"userAgent"`
	}
	if jsonErr := json.Unmarshal(raw, &initResult); jsonErr != nil || initResult.UserAgent == "" {
		slog.Warn("codex-remote initialize response carried no readable userAgent; version gate stays closed",
			"decodeErr", jsonErr)
	} else {
		p.agent.NoteServerUserAgent(initResult.UserAgent)
	}
	if err := cl.Notify("initialized", map[string]any{}); err != nil {
		_ = cl.Close()
		return err
	}
	go keepStreamAlive(cl, stream)
	return nil
}

const (
	pingInterval    = 10 * time.Second
	streamIdleLimit = 60 * time.Second
)

// streamHealthCheck reports why the stream looks dead: inbound silence beyond
// the idle limit (missed pongs and no traffic) or a failed ping write. nil
// means healthy. The caller owns failing the stream.
func streamHealthCheck(cl *Client, stream *Stream) error {
	if cl.IsClosed() {
		return nil
	}
	if idle := stream.IdleFor(); idle > streamIdleLimit {
		return fmt.Errorf("codex-remote: stream idle %s without inbound traffic", idle.Truncate(time.Second))
	}
	return stream.Ping()
}

func keepStreamAlive(cl *Client, stream *Stream) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for range ticker.C {
		if err := streamHealthCheck(cl, stream); err != nil {
			slog.Warn("codex-remote keepalive failing stream", "error", err)
			stream.fail(err)
			return
		}
	}
}
