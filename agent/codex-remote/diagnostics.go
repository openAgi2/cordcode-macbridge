package codexremote

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	ReadinessAvailable         = "available"
	ReadinessPairingRequired   = "pairing_required"
	ReadinessServiceNotRunning = "service_not_running"
	// ReadinessNotDetected / ReadinessNotLoggedIn reuse existing wire enum
	// values (agent_descriptor.go:20-34) — the absence classes of P2 plan
	// §3 D1. iOS shows the reason text through the generic not-available
	// branch (raw String decode); the Mac row has its own copy per status.
	ReadinessNotDetected = "not_detected"
	ReadinessNotLoggedIn = "not_logged_in"
)

// restoreAbsenceClass returns the class of the most recent restoreOnce
// failure ("" once a restore succeeded or before the first attempt).
func (p *PairingController) restoreAbsenceClass() restoreErrClass {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastRestoreClass
}

// StructuredInstanceReadiness is the descriptor-facing, read-only readiness
// seam. It preserves the distinction between a missing/revoked controller
// identity, an enrolled controller whose dependency is absent, and one
// whose Desktop environment is merely offline (P2 plan §3 D1):
// not_installed → not_detected, not_logged_in → not_logged_in, everything
// else (desktop offline, transient, or class cleared after a successful
// restore) → service_not_running, fail-closed — never not_configured.
func (a *Agent) StructuredInstanceReadiness() (string, string) {
	a.mu.Lock()
	cl := a.client
	paired := a.paired
	a.mu.Unlock()
	if cl != nil && !cl.IsClosed() {
		return ReadinessAvailable, "codex-remote controller protocol " + ProtocolVersion + " (environment stream bound)"
	}
	snap := a.pairingSnapshot()
	if paired || (a.pairing != nil && a.pairing.hasPersistedIdentity()) {
		detail := snap.Message
		if detail == "" {
			detail = "已配对，等待 ChatGPT Desktop"
		}
		var class restoreErrClass
		if a.pairing != nil {
			class = a.pairing.restoreAbsenceClass()
		}
		switch class {
		case errClassNotInstalled:
			return ReadinessNotDetected, detail
		case errClassNotLoggedIn:
			return ReadinessNotLoggedIn, detail
		default:
			return ReadinessServiceNotRunning, detail
		}
	}
	return ReadinessPairingRequired, ErrNotConfigured.Error()
}

// InstanceStatus is the legacy boolean mirror used by callers that only need
// availability. Descriptor consumers use StructuredInstanceReadiness so the
// pairing-required and service-offline causes do not collapse together.
func (a *Agent) InstanceStatus() (bool, string) {
	status, detail := a.StructuredInstanceReadiness()
	return status == ReadinessAvailable, detail
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
