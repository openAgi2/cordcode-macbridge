package codexremote

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// InstanceStatus is a read-only mirror. Phase 1 identity has no enrollment
// yet, so the backend is unavailable with a truthful not_configured detail.
func (a *Agent) InstanceStatus() (bool, string) {
	a.mu.Lock()
	cl := a.client
	paired := a.paired
	a.mu.Unlock()
	if cl != nil && !cl.IsClosed() {
		return true, "codex-remote controller protocol " + ProtocolVersion + " (environment stream bound)"
	}
	snap := a.pairingSnapshot()
	if paired {
		if snap.Message != "" {
			return false, snap.Message
		}
		return false, "已配对，等待 ChatGPT Desktop"
	}
	if snap.Message != "" {
		return false, snap.Message
	}
	return false, ErrNotConfigured.Error()
}

func (a *Agent) StartRemoteControl(ctx context.Context, token, accountID string) (PairingSnapshot, error) {
	if a.pairing == nil {
		a.pairing = newPairingController(a)
	}
	if a.pairing.hasPersistedIdentity() {
		return a.pairing.reconnectFromStore(ctx)
	}
	if token != "" && accountID != "" {
		return a.pairing.Start(ctx, token, accountID)
	}
	authToken, acc, err := loadChatGPTAuth(ctx)
	if err != nil {
		return PairingSnapshot{Phase: PairPhaseFailed, Message: err.Error()}, err
	}
	authorizeURL, wait, err := a.pairing.startStepUp(acc)
	if err != nil {
		return PairingSnapshot{Phase: PairPhaseFailed, Message: "无法开始浏览器授权"}, err
	}
	_ = openBrowser(authorizeURL)
	a.pairing.mu.Lock()
	a.pairing.state.phase = PairPhaseAuthorizing
	a.pairing.state.stepUpURL = authorizeURL
	a.pairing.state.message = "请在打开的浏览器完成授权，然后回到这里输入电脑配对码"
	a.pairing.mu.Unlock()
	go func() {
		waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		stepTok, err := wait(waitCtx)
		if err != nil {
			slog.Warn("codex-remote pairing step-up failed")
			a.pairing.setFailed("浏览器授权失败或超时")
			return
		}
		a.pairing.stepUpToken = stepTok
		slog.Info("codex-remote pairing", "phase", "step_up_complete")
		if _, err := a.pairing.Start(waitCtx, authToken, acc); err != nil {
			slog.Warn("codex-remote pairing enroll failed")
		}
	}()
	return a.pairing.Snapshot(), nil
}

func (a *Agent) SubmitRemoteControlCode(ctx context.Context, code string) (PairingSnapshot, error) {
	if a.pairing == nil {
		return PairingSnapshot{Phase: PairPhaseFailed, Message: "尚未开始配对"}, fmt.Errorf("pairing not started")
	}
	return a.pairing.SubmitCode(ctx, code)
}

func (a *Agent) RemoteControlStatus() PairingSnapshot {
	return a.pairingSnapshot()
}
