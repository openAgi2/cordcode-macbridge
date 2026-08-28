package codexremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var officialAPIBase = "https://chatgpt.com/backend-api"

const (
	PairPhaseIdle         = "idle"
	PairPhaseAuthorizing  = "authorizing"
	PairPhaseAwaitingCode = "awaiting_code"
	PairPhaseReady        = "ready"
	PairPhaseOffline      = "offline"
	PairPhaseFailed       = "failed"
)

var errPairingRevoked = errors.New("codex-remote: pairing revoked")

// PairingSnapshot is safe for Management API / UI. No tokens or pairing codes.
type PairingSnapshot struct {
	Phase      string `json:"phase"`
	StepUpURL  string `json:"stepUpUrl,omitempty"`
	Message    string `json:"message,omitempty"`
	Online     bool   `json:"online,omitempty"`
	ClientType string `json:"clientType,omitempty"`
}

type remoteEnv struct {
	EnvID      string `json:"env_id"`
	Online     bool   `json:"online"`
	ClientType string `json:"client_type"`
	OS         string `json:"os"`
}

type pairState struct {
	phase     string
	stepUpURL string
	message   string
	token     string
	accountID string
	clientID  string
	ctrlToken string
	ctrlExp   string
	key       *deviceKey
	env       *remoteEnv
	startRaw  json.RawMessage
}

// PairingController runs ChatGPT Remote Control enroll/pair for this agent.
type PairingController struct {
	agent       *Agent
	keys        *deviceKeyStore
	http        *http.Client
	open        func(string) error
	stepUpToken string
	authTokenFn func(context.Context) (token, accountID string, err error)
	bindEnv     func(context.Context, *remoteEnv) error
	storePath   string

	mu        sync.Mutex
	restoreMu sync.Mutex
	state     pairState
}

func newPairingController(agent *Agent) *PairingController {
	return &PairingController{
		agent: agent,
		keys:  newDeviceKeyStore(),
		http:  &http.Client{Timeout: 20 * time.Second},
		open:  func(string) error { return fmt.Errorf("browser open not configured") },
		state: pairState{phase: PairPhaseIdle, message: "尚未配对 ChatGPT Desktop"},
	}
}

func (p *PairingController) Snapshot() PairingSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	snap := PairingSnapshot{Phase: p.state.phase, StepUpURL: p.state.stepUpURL, Message: p.state.message}
	if p.state.env != nil {
		snap.Online = p.state.env.Online
		snap.ClientType = p.state.env.ClientType
	}
	return snap
}

func (p *PairingController) setFailed(msg string) PairingSnapshot {
	p.mu.Lock()
	p.state.phase = PairPhaseFailed
	p.state.message = msg
	p.state.stepUpURL = ""
	p.mu.Unlock()
	slog.Warn("codex-remote pairing", "phase", PairPhaseFailed, "message", msg)
	return p.Snapshot()
}

func (p *PairingController) markOffline(msg string) PairingSnapshot {
	p.mu.Lock()
	p.state.phase = PairPhaseOffline
	p.state.message = msg
	p.state.stepUpURL = ""
	p.mu.Unlock()
	if p.agent != nil {
		p.agent.mu.Lock()
		p.agent.paired = true
		p.agent.mu.Unlock()
	}
	slog.Info("codex-remote pairing", "phase", PairPhaseOffline, "message", msg)
	return p.Snapshot()
}

func (p *PairingController) chatGPTAuth(ctx context.Context) (string, string, error) {
	if p.authTokenFn != nil {
		return p.authTokenFn(ctx)
	}
	return loadChatGPTAuth(ctx)
}

func (p *PairingController) hasPersistedIdentity() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	ok := p.state.clientID != "" && p.state.key != nil
	path := p.storePath
	p.mu.Unlock()
	if ok {
		return true
	}
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func (p *PairingController) forgetPersistedPairing() {
	if p == nil || p.storePath == "" {
		return
	}
	_ = os.Remove(p.storePath)
}

func (p *PairingController) jsonRequest(ctx context.Context, token, accountID, method, path string, body any) (json.RawMessage, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, officialAPIBase+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-Id", accountID)
	req.Header.Set("originator", "Codex Desktop")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Warn("codex-remote official request failed", "method", method, "path", path, "status", resp.StatusCode)
		return raw, resp.StatusCode, fmt.Errorf("official request failed with HTTP %d on %s %s", resp.StatusCode, method, path)
	}
	if len(raw) == 0 {
		return json.RawMessage("null"), resp.StatusCode, nil
	}
	return json.RawMessage(raw), resp.StatusCode, nil
}

func (p *PairingController) Start(ctx context.Context, token, accountID string) (PairingSnapshot, error) {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(accountID) == "" {
		return p.setFailed("请先打开并登录本机 ChatGPT Desktop"), fmt.Errorf("codex-remote: missing ChatGPT auth")
	}
	p.mu.Lock()
	p.state = pairState{phase: PairPhaseAuthorizing, token: token, accountID: accountID, message: "正在向 ChatGPT 注册这台控制器"}
	p.mu.Unlock()

	startRaw, _, err := p.jsonRequest(ctx, token, accountID, http.MethodPost, "/codex/remote/control/client/enroll/start", map[string]any{})
	if err != nil {
		return p.setFailed("注册控制器失败，请确认已登录 ChatGPT"), err
	}
	var start struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(startRaw, &start); err != nil || start.ClientID == "" {
		return p.setFailed("注册控制器返回无法识别"), fmt.Errorf("enroll/start schema")
	}
	key, err := p.keys.Create()
	if err != nil {
		return p.setFailed("无法创建本机配对密钥"), err
	}
	p.mu.Lock()
	p.state.clientID = start.ClientID
	p.state.key = key
	p.state.startRaw = startRaw
	p.mu.Unlock()
	if p.stepUpToken != "" {
		if err := p.enrollFinish(ctx, startRaw); err != nil {
			return p.setFailed("完成授权失败，请重新配对"), err
		}
	}
	p.mu.Lock()
	p.state.phase = PairPhaseAwaitingCode
	p.state.message = "在 ChatGPT Desktop「控制这台 Mac」切换到「电脑」，把配对码填到这里"
	p.mu.Unlock()
	slog.Info("codex-remote pairing", "phase", PairPhaseAwaitingCode)
	return p.Snapshot(), nil
}

func (p *PairingController) enrollFinish(ctx context.Context, startRaw json.RawMessage) error {
	p.mu.Lock()
	token := p.state.token
	accountID := p.state.accountID
	clientID := p.state.clientID
	key := p.state.key
	stepUp := p.stepUpToken
	p.mu.Unlock()
	if key == nil {
		return fmt.Errorf("missing device key")
	}
	if stepUp == "" {
		// Tests and the Mac flow inject a step-up token. Without it we still
		// allow pair() to be attempted after Start, but enroll/finish needs it.
		return fmt.Errorf("missing step-up token")
	}
	var start struct {
		ClientID  string          `json:"client_id"`
		Challenge json.RawMessage `json:"device_key_challenge"`
	}
	if err := json.Unmarshal(startRaw, &start); err != nil {
		return err
	}
	var challenge map[string]any
	if err := json.Unmarshal(start.Challenge, &challenge); err != nil {
		return err
	}
	proof, err := p.deviceKeyProof(challenge, key, false)
	if err != nil {
		return err
	}
	finishRaw, _, err := p.jsonRequest(ctx, token, accountID, http.MethodPost, "/codex/remote/control/client/enroll/finish", map[string]any{
		"client_id":     clientID,
		"step_up_token": stepUp,
		"device_identity": map[string]any{
			"key_id":                     key.KeyID,
			"public_key_spki_der_base64": key.PublicKeySpkiDerBase64,
			"algorithm":                  key.Algorithm,
			"protection_class":           key.ProtectionClass,
		},
		"device_key_proof": proof,
	})
	if err != nil {
		return err
	}
	return p.applyCtrlToken(finishRaw, "enroll/finish")
}

func (p *PairingController) deviceKeyProof(challenge map[string]any, key *deviceKey, requireIdentityHash bool) (map[string]any, error) {
	if key == nil {
		return nil, fmt.Errorf("missing device key")
	}
	hash := key.identityHash()
	if requireIdentityHash {
		chHash, _ := challenge["device_identity_hash"].(string)
		if chHash == "" {
			return nil, fmt.Errorf("refresh challenge missing device identity hash")
		}
		if chHash != hash {
			return nil, fmt.Errorf("refresh challenge identity mismatch")
		}
		hash = chHash
	}
	payload := map[string]any{
		"accountUserId":                 challenge["account_user_id"],
		"audience":                      "remote_control_client_enrollment",
		"challengeExpiresAt":            challenge["challenge_expires_at"],
		"challengeId":                   challenge["challenge_id"],
		"clientId":                      challenge["client_id"],
		"deviceIdentitySha256Base64url": hash,
		"nonce":                         challenge["nonce"],
		"targetOrigin":                  challenge["target_origin"],
		"targetPath":                    challenge["target_path"],
		"type":                          "remoteControlClientEnrollment",
	}
	signedPayload, signature, err := p.keys.signEnrollment(key, payload)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"challenge_token":       challenge["challenge_token"],
		"key_id":                key.KeyID,
		"signature_der_base64":  signature,
		"signed_payload_base64": signedPayload,
		"algorithm":             key.Algorithm,
	}, nil
}

func (p *PairingController) applyCtrlToken(raw json.RawMessage, schemaName string) error {
	var finish struct {
		RemoteControlToken string          `json:"remote_control_token"`
		ExpiresAt          json.RawMessage `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &finish); err != nil || finish.RemoteControlToken == "" {
		return fmt.Errorf("%s schema", schemaName)
	}
	ctrlExp := strings.TrimSpace(string(finish.ExpiresAt))
	ctrlExp = strings.Trim(ctrlExp, `"`)
	p.mu.Lock()
	p.state.ctrlToken = finish.RemoteControlToken
	p.state.ctrlExp = ctrlExp
	p.mu.Unlock()
	return nil
}

func (p *PairingController) refreshControlToken(ctx context.Context) error {
	p.mu.Lock()
	token := p.state.token
	accountID := p.state.accountID
	clientID := p.state.clientID
	key := p.state.key
	p.mu.Unlock()
	if token == "" || accountID == "" || clientID == "" || key == nil {
		return fmt.Errorf("pairing state incomplete")
	}
	startRaw, status, err := p.jsonRequest(ctx, token, accountID, http.MethodPost, "/codex/remote/control/client/refresh/start", map[string]any{
		"client_id": clientID,
	})
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return errPairingRevoked
	}
	if err != nil {
		return err
	}
	var start struct {
		ClientID  string          `json:"client_id"`
		Challenge json.RawMessage `json:"device_key_challenge"`
	}
	if json.Unmarshal(startRaw, &start) != nil || start.ClientID != clientID || len(start.Challenge) == 0 {
		return fmt.Errorf("refresh/start schema")
	}
	var challenge map[string]any
	if err := json.Unmarshal(start.Challenge, &challenge); err != nil {
		return err
	}
	proof, err := p.deviceKeyProof(challenge, key, true)
	if err != nil {
		return err
	}
	finishRaw, status, err := p.jsonRequest(ctx, token, accountID, http.MethodPost, "/codex/remote/control/client/refresh/finish", map[string]any{
		"client_id":        clientID,
		"device_key_proof": proof,
	})
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return errPairingRevoked
	}
	if err != nil {
		return err
	}
	if err := p.applyCtrlToken(finishRaw, "refresh/finish"); err != nil {
		return err
	}
	_ = p.savePersistedPairing()
	slog.Info("codex-remote pairing", "phase", "token_refreshed")
	return nil
}

func (p *PairingController) lookupDesktopEnv(ctx context.Context, preferID string) (*remoteEnv, error) {
	p.mu.Lock()
	token := p.state.token
	accountID := p.state.accountID
	clientID := p.state.clientID
	p.mu.Unlock()
	if token == "" || clientID == "" {
		return nil, fmt.Errorf("pairing state incomplete")
	}
	raw, _, err := p.jsonRequest(ctx, token, accountID, http.MethodGet, "/codex/remote/control/clients/"+clientID+"/environments?limit=100", nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []remoteEnv `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("environment list schema")
	}
	if preferID != "" {
		for i := range list.Items {
			item := list.Items[i]
			if item.EnvID == preferID && item.Online {
				cp := item
				return &cp, nil
			}
		}
	}
	env := pickDesktopEnv(list.Items)
	if env == nil {
		return nil, fmt.Errorf("no desktop environment")
	}
	return env, nil
}

func (p *PairingController) bindEnvironment(ctx context.Context, env *remoteEnv) error {
	if p.bindEnv != nil {
		return p.bindEnv(ctx, env)
	}
	return p.bindLive(ctx, env)
}

func (p *PairingController) SubmitCode(ctx context.Context, code string) (PairingSnapshot, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 256 {
		snap := p.Snapshot()
		snap.Message = "请输入电脑配对码"
		return snap, fmt.Errorf("invalid pairing code")
	}
	p.mu.Lock()
	phase := p.state.phase
	token := p.state.token
	accountID := p.state.accountID
	clientID := p.state.clientID
	p.mu.Unlock()
	if phase != PairPhaseAwaitingCode {
		snap := p.Snapshot()
		if phase == PairPhaseIdle {
			snap.Message = "请先点击配对，再输入电脑码"
		} else if snap.Message == "" {
			snap.Message = "请先完成浏览器授权，再输入电脑码"
		}
		return snap, fmt.Errorf("pairing is not waiting for a code")
	}
	if token == "" || clientID == "" {
		snap := p.Snapshot()
		snap.Message = "请先点击配对，再输入电脑码"
		return snap, fmt.Errorf("pairing not started")
	}
	p.mu.Lock()
	needFinish := p.state.ctrlToken == "" && p.stepUpToken != "" && len(p.state.startRaw) > 0
	startRaw := p.state.startRaw
	p.mu.Unlock()
	if needFinish {
		if err := p.enrollFinish(ctx, startRaw); err != nil {
			return p.setFailed("完成授权失败，请重新配对"), err
		}
	}
	_, _, err := p.jsonRequest(ctx, token, accountID, http.MethodPost, "/wham/remote/control/client/pair", map[string]any{
		"client_id":           clientID,
		"manual_pairing_code": code,
	})
	if err != nil {
		return p.setFailed("配对码无效或已过期，请在 Desktop 刷新后再试"), err
	}
	var env *remoteEnv
	for attempt := 0; attempt < 10; attempt++ {
		raw, _, err := p.jsonRequest(ctx, token, accountID, http.MethodGet, "/codex/remote/control/clients/"+clientID+"/environments?limit=100", nil)
		if err != nil {
			return p.setFailed("读取已配对电脑失败"), err
		}
		var list struct {
			Items []remoteEnv `json:"items"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return p.setFailed("配对结果无法识别"), err
		}
		env = pickDesktopEnv(list.Items)
		if env != nil {
			break
		}
		time.Sleep(time.Second)
	}
	if env == nil {
		return p.setFailed("没有找到在线的 ChatGPT Desktop，请确认 Desktop 已打开并允许远程控制"), fmt.Errorf("no desktop environment")
	}
	p.mu.Lock()
	p.state.env = env
	p.mu.Unlock()
	if err := p.bindEnvironment(ctx, env); err != nil {
		slog.Warn("codex-remote pairing stream bind failed")
		return p.setFailed("已配对但未能连上 Desktop 数据面，请重试配对"), err
	}
	p.mu.Lock()
	p.state.env = env
	p.state.phase = PairPhaseReady
	p.state.message = "已连接到 ChatGPT Desktop"
	p.mu.Unlock()
	p.agent.markPaired()
	p.persistPairingOrLog()
	return p.Snapshot(), nil
}

func pickDesktopEnv(items []remoteEnv) *remoteEnv {
	var fallback *remoteEnv
	n := 0
	for i := range items {
		item := items[i]
		if item.Online && item.ClientType == "CODEX_DESKTOP_APP" {
			n++
			if n == 1 {
				cp := item
				fallback = &cp
			} else {
				return nil
			}
		}
	}
	if fallback != nil {
		return fallback
	}
	if len(items) == 1 && items[0].Online {
		cp := items[0]
		return &cp
	}
	return nil
}

func (a *Agent) markPaired() {
	a.mu.Lock()
	a.paired = true
	a.mu.Unlock()
}

func (a *Agent) pairingSnapshot() PairingSnapshot {
	if a.pairing == nil {
		return PairingSnapshot{Phase: PairPhaseIdle, Message: "尚未配对 ChatGPT Desktop"}
	}
	return a.pairing.Snapshot()
}
