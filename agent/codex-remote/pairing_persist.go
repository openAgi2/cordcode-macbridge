package codexremote

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const pairingStoreName = "codex-remote-pairing.json"

type persistedPairing struct {
	ClientID               string `json:"clientId"`
	EnvID                  string `json:"envId"`
	ClientType             string `json:"clientType"`
	CtrlExp                string `json:"ctrlExp"`
	CtrlToken              string `json:"ctrlToken"`
	KeyID                  string `json:"keyId"`
	PrivateKeyPKCS8Base64  string `json:"privateKeyPkcs8"`
	PublicKeySpkiDerBase64 string `json:"publicKeySpki"`
	Algorithm              string `json:"algorithm"`
	ProtectionClass        string `json:"protectionClass"`
}

func pairingStorePath(dataDir string) string {
	return filepath.Join(dataDir, pairingStoreName)
}

func (p *PairingController) persistPairingOrLog() {
	if err := p.savePersistedPairing(); err != nil {
		slog.Warn("codex-remote pairing persist failed", "error", err)
		return
	}
	if p != nil && p.storePath != "" {
		slog.Info("codex-remote pairing", "phase", "persisted")
	}
}

func (p *PairingController) savePersistedPairing() error {
	if p == nil || p.storePath == "" {
		return nil
	}
	p.mu.Lock()
	envID, clientType := "", ""
	if p.state.env != nil {
		envID = p.state.env.EnvID
		clientType = p.state.env.ClientType
	}
	rec := persistedPairing{
		ClientID:   p.state.clientID,
		EnvID:      envID,
		ClientType: clientType,
		CtrlExp:    p.state.ctrlExp,
		CtrlToken:  p.state.ctrlToken,
	}
	key := p.state.key
	p.mu.Unlock()
	if rec.ClientID == "" || rec.EnvID == "" || rec.CtrlToken == "" || key == nil || key.private == nil {
		return fmt.Errorf("pairing state incomplete")
	}
	der, err := x509.MarshalPKCS8PrivateKey(key.private)
	if err != nil {
		return err
	}
	rec.KeyID = key.KeyID
	rec.PrivateKeyPKCS8Base64 = base64.StdEncoding.EncodeToString(der)
	rec.PublicKeySpkiDerBase64 = key.PublicKeySpkiDerBase64
	rec.Algorithm = key.Algorithm
	rec.ProtectionClass = key.ProtectionClass
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p.storePath, raw, 0o600); err != nil {
		return err
	}
	return nil
}

func (p *PairingController) loadPersistedPairing() (*persistedPairing, *deviceKey, error) {
	if p == nil || p.storePath == "" {
		return nil, nil, fmt.Errorf("pairing store not configured")
	}
	raw, err := os.ReadFile(p.storePath)
	if err != nil {
		return nil, nil, err
	}
	var rec persistedPairing
	if json.Unmarshal(raw, &rec) != nil || rec.ClientID == "" || rec.EnvID == "" || rec.CtrlToken == "" || rec.PrivateKeyPKCS8Base64 == "" {
		return nil, nil, fmt.Errorf("pairing store incomplete")
	}
	der, err := base64.StdEncoding.DecodeString(rec.PrivateKeyPKCS8Base64)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, nil, err
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("pairing store key type")
	}
	key := &deviceKey{
		KeyID:                  rec.KeyID,
		PublicKeySpkiDerBase64: rec.PublicKeySpkiDerBase64,
		Algorithm:              rec.Algorithm,
		ProtectionClass:        rec.ProtectionClass,
		private:                priv,
	}
	return &rec, key, nil
}

func (p *PairingController) reconnectFromStore(ctx context.Context) (PairingSnapshot, error) {
	if err := p.restoreOnce(ctx); err != nil {
		if errors.Is(err, errPairingRevoked) {
			p.forgetPersistedPairing()
			return p.setFailed("配对已失效，请重新配对 Codex Desktop"), err
		}
		msg := "已配对，等待 ChatGPT Desktop"
		if err.Error() == "请打开并登录 ChatGPT Desktop" || err.Error() == "请先安装并登录 ChatGPT Desktop" || err.Error() == "ChatGPT 未登录" || err.Error() == "读取 ChatGPT 登录态超时" {
			msg = "请打开并登录 ChatGPT Desktop"
		} else if err.Error() == "no desktop environment" {
			msg = "请打开 ChatGPT Desktop"
		}
		return p.markOffline(msg), err
	}
	return p.Snapshot(), nil
}

func (p *PairingController) restoreOnce(ctx context.Context) error {
	p.restoreMu.Lock()
	defer p.restoreMu.Unlock()
	if p.agent != nil {
		p.agent.mu.Lock()
		cl := p.agent.client
		stopped := p.agent.stopped
		p.agent.mu.Unlock()
		if stopped {
			return fmt.Errorf("agent stopped")
		}
		if cl != nil && !cl.IsClosed() {
			p.mu.Lock()
			p.state.phase = PairPhaseReady
			p.state.message = "已连接到 ChatGPT Desktop"
			p.mu.Unlock()
			return nil
		}
	}
	rec, key, err := p.loadPersistedPairing()
	if err != nil {
		return err
	}
	p.keys.Put(key)
	p.mu.Lock()
	p.state.clientID = rec.ClientID
	// stream_id is connection-epoch state, never enrollment state. Older stores
	// may still contain a streamId field; encoding/json ignores it on read.
	p.state.streamID = ""
	p.state.ctrlToken = rec.CtrlToken
	p.state.ctrlExp = rec.CtrlExp
	p.state.key = key
	p.state.env = &remoteEnv{EnvID: rec.EnvID, Online: false, ClientType: rec.ClientType}
	p.state.phase = PairPhaseAuthorizing
	p.state.message = "正在恢复与 ChatGPT Desktop 的连接"
	p.mu.Unlock()
	if p.agent != nil {
		p.agent.mu.Lock()
		p.agent.paired = true
		p.agent.mu.Unlock()
	}
	token, accountID, err := p.chatGPTAuth(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.state.token = token
	p.state.accountID = accountID
	p.mu.Unlock()
	if err := p.refreshControlToken(ctx); err != nil {
		if errors.Is(err, errPairingRevoked) {
			return err
		}
		slog.Warn("codex-remote pairing token refresh failed; trying saved token")
	}
	env, err := p.lookupDesktopEnv(ctx, rec.EnvID)
	if err != nil {
		return err
	}
	if err := p.bindEnvironment(ctx, env); err != nil {
		return err
	}
	p.mu.Lock()
	p.state.env = env
	p.state.phase = PairPhaseReady
	p.state.message = "已连接到 ChatGPT Desktop"
	p.mu.Unlock()
	if p.agent != nil {
		p.agent.markPaired()
	}
	slog.Info("codex-remote pairing", "phase", "restored")
	return nil
}

func (a *Agent) restorePersistedPairing() {
	p := a.pairing
	if p == nil || p.storePath == "" {
		return
	}
	if _, err := os.Stat(p.storePath); err == nil {
		backoff := newReconnectBackoff()
		for {
			a.mu.Lock()
			stopped := a.stopped
			a.mu.Unlock()
			if stopped {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			err := p.restoreOnce(ctx)
			cancel()
			if err == nil {
				break
			}
			if errors.Is(err, errPairingRevoked) {
				p.forgetPersistedPairing()
				p.setFailed("配对已失效，请重新配对 Codex Desktop")
				return
			}
			msg := "已配对，等待 ChatGPT Desktop"
			if err.Error() == "请打开并登录 ChatGPT Desktop" || err.Error() == "请先安装并登录 ChatGPT Desktop" || err.Error() == "ChatGPT 未登录" || err.Error() == "读取 ChatGPT 登录态超时" {
				msg = "请打开并登录 ChatGPT Desktop"
			} else if err.Error() == "no desktop environment" {
				msg = "请打开 ChatGPT Desktop"
			}
			p.markOffline(msg)
			retryIn := backoff.Next()
			slog.Warn("codex-remote pairing restore waiting", "retryIn", retryIn)
			a.sleepInterruptible(retryIn)
		}
	}
	a.watchBinding()
}

func (a *Agent) watchBinding() {
	backoff := newReconnectBackoff()
	for {
		a.mu.Lock()
		stopped := a.stopped
		cl := a.client
		a.mu.Unlock()
		if stopped {
			return
		}
		if cl != nil && !cl.IsClosed() {
			for {
				time.Sleep(2 * time.Second)
				a.mu.Lock()
				stopped = a.stopped
				still := a.client
				a.mu.Unlock()
				if stopped {
					return
				}
				if still == nil || still.IsClosed() {
					slog.Warn("codex-remote pairing stream lost; reconnecting")
					break
				}
			}
		}
		if a.pairing == nil || !a.pairing.hasPersistedIdentity() {
			a.sleepInterruptible(2 * time.Second)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		err := a.pairing.restoreOnce(ctx)
		cancel()
		if err == nil {
			backoff.Reset()
			continue
		}
		if errors.Is(err, errPairingRevoked) {
			a.pairing.forgetPersistedPairing()
			a.pairing.setFailed("配对已失效，请重新配对 Codex Desktop")
			return
		}
		a.pairing.markOffline("已配对，等待 ChatGPT Desktop")
		retryIn := backoff.Next()
		slog.Warn("codex-remote pairing reconnect waiting", "retryIn", retryIn)
		a.sleepInterruptible(retryIn)
	}
}
