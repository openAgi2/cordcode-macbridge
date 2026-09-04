package claudecode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// control_ops.go —— 会话内控制操作（设计 §6 Phase 2，S8 裁决方案 a）。
//
// 依据 Phase 0 证据（CLI 2.1.234，scripts/claudecode-phase0/dumps/）：
//   - set_model 成功体 = 裸 {subtype:success, request_id}；'default'/null 重置
//   - set_permission_mode 成功体 = {"mode":…}；生效确认另走 system/status 广播帧
//   - interrupt(cancel_queued:true) 成功体 = {still_queued:[], cancelled:[]}
//   - capabilities 挂在 system/init（仅首个 turn 时出现）：
//     [interrupt_receipt_v1, interrupt_cancel_queued_v1, msg_lifecycle_v1]
//
// S8 裁决（owner 2026-09-04）：interrupt = 停 turn、留进程；会话真正关闭时仍走
// 既有 Close 路径（stdin EOF → Stop hooks）。

// opTimeout bounds one session-scoped control operation.
const opTimeout = 15 * time.Second

// errControlUnsupported marks "this backend session cannot take the official
// path"（无能力位 / CLI 拒收）——调用方回落既有实现，不是失败冒充成功。
var errControlUnsupported = errors.New("claude control op unsupported on this session")

// controlError carries a CLI-rejected control_response (subtype:"error").
type controlError struct {
	subtype string
	message string
}

func (e *controlError) Error() string {
	return fmt.Sprintf("claude control response error: %s", e.message)
}

// sendControlExpectSuccess sends one control request and requires a success
// response; error subtype / timeout are returned as errors (fail visibly).
func (cs *claudeSession) sendControlExpectSuccess(ctx context.Context, inner map[string]any) (map[string]any, error) {
	cctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	resp, err := cs.sendControlRequest(cctx, inner)
	if err != nil {
		return nil, err
	}
	if resp.Subtype != "success" {
		msg, _ := resp.Raw["message"].(string)
		if msg == "" {
			msg, _ = resp.Raw["error"].(string)
		}
		return nil, &controlError{subtype: resp.Subtype, message: msg}
	}
	payload, _ := resp.Raw["response"].(map[string]any)
	return payload, nil
}

// ---- capabilities（system/init，随首个 turn 出现） -----------------------

func (cs *claudeSession) storeInitCapabilities(raw map[string]any) {
	caps, ok := raw["capabilities"].([]any)
	if !ok {
		return
	}
	set := make(map[string]struct{}, len(caps))
	for _, c := range caps {
		if s, ok := c.(string); ok {
			set[s] = struct{}{}
		}
	}
	cs.initCapabilities.Store(set)
}

func (cs *claudeSession) hasCapability(name string) bool {
	v, ok := cs.initCapabilities.Load().(map[string]struct{})
	if !ok {
		return false
	}
	_, has := v[name]
	return has
}

// ---- set_model（设计 §6 Phase 2.2） --------------------------------------

// SetModelLive switches the model of the RUNNING session through the official
// set_model control request（替换「下次 spawn 生效」）。model 为空或 "default"
// 时按官方语义重置为会话默认模型。CLI 拒收/超时返回错误——不伪装成功。
func (cs *claudeSession) SetModelLive(ctx context.Context, model string) error {
	if !cs.alive.Load() {
		return errControlUnsupported
	}
	inner := map[string]any{"subtype": "set_model"}
	switch model {
	case "", "default", "null":
		inner["model"] = "default" // SDK 语义：省略/null/'default' 均为重置
	default:
		inner["model"] = model
	}
	if _, err := cs.sendControlExpectSuccess(ctx, inner); err != nil {
		return err
	}
	if model != "" && model != "default" {
		cs.model = model
	}
	return nil
}

// ---- set_permission_mode（受限，设计 §6 Phase 2.3 / M3） ------------------

// claudeLivePermissionModes is the restricted live-switch set. plan/auto 在活会话
// 禁止（SetLiveMode 语义维持）；ExitPlanMode 批准保持纯 allow 不经此路径。
var claudeLivePermissionModes = map[string]struct{}{
	"default":           {},
	"acceptEdits":       {},
	"bypassPermissions": {},
	"dontAsk":           {},
}

// SetPermissionModeLive sends the official set_permission_mode control frame
// for the restricted four modes. Returns errControlUnsupported for plan/auto
// （受限四档之外不新增旁路）or a dead session; CLI 拒收返回 controlError。
// 成功后本地 auto-answer 标志同步（作为缺位回退的同一状态位，避免双应答）。
func (cs *claudeSession) SetPermissionModeLive(ctx context.Context, mode string) error {
	if _, ok := claudeLivePermissionModes[mode]; !ok {
		return fmt.Errorf("%w: live permission mode %q not in restricted set", errControlUnsupported, mode)
	}
	if !cs.alive.Load() {
		return errControlUnsupported
	}
	payload, err := cs.sendControlExpectSuccess(ctx, map[string]any{
		"subtype": "set_permission_mode",
		"mode":    mode,
	})
	if err != nil {
		return err
	}
	// 成功体回显 {"mode":…}（Phase 0 dump req_5/req_6）；以回显为准同步本地状态。
	if echoed, _ := payload["mode"].(string); echoed != "" {
		cs.setPermissionMode(echoed)
	} else {
		cs.setPermissionMode(mode)
	}
	return nil
}

// ---- interrupt（S8 裁决方案 a：停 turn、留进程） --------------------------

// CancelTurn implements core.TurnCanceler with the OFFICIAL interrupt control
// request (cancel_queued:true — iOS Stop 语义一轮往返停整个会话队列)。
//
// 能力门（fail closed）：system/init 的 capabilities 含 interrupt_receipt_v1 才
// 走官方路径；能力位缺失（老 CLI / 尚无 turn）返回 errControlUnsupported，
// 调用方回落既有 abort→Close 路径——禁止把合成 aborted 冒充官方回执。
//
// S8(a)：成功后进程保留；被打断 turn 的官方 result 帧走既有 handleResult 收口。
func (cs *claudeSession) CancelTurn(ctx context.Context) error {
	if !cs.alive.Load() {
		return errControlUnsupported
	}
	if !cs.hasCapability("interrupt_receipt_v1") {
		return fmt.Errorf("%w: interrupt_receipt_v1 not advertised (capabilities unseen or absent)", errControlUnsupported)
	}
	cctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	resp, err := cs.sendControlRequest(cctx, map[string]any{
		"subtype":       "interrupt",
		"cancel_queued": true,
	})
	if err != nil {
		return err
	}
	if resp.Subtype != "success" {
		msg, _ := resp.Raw["message"].(string)
		return &controlError{subtype: resp.Subtype, message: msg}
	}
	// 回执形状（Phase 0 dump req_7）：{"still_queued":[…],"cancelled":[…]}。
	// 2.1.234 已带 cancelled（interrupt_cancel_queued_v1）；older CLI 可能只有
	// still_queued——两者都只记录，不作为成功判据（success subtype 已判定）。
	stillQueued, _ := resp.Raw["still_queued"].([]any)
	cancelled, _ := resp.Raw["cancelled"].([]any)
	slog.Info("claudeSession: official interrupt receipt",
		"still_queued", len(stillQueued), "cancelled", len(cancelled))
	return nil
}

// ---- system/status 广播同步 ----------------------------------------------

// syncPermissionModeFromStatus keeps the local permission-mode atomics truthful
// after ANY switch path (official control frame, CLI-internal /permission, or
// external state changes) — the CLI broadcasts system/status{permissionMode}.
func (cs *claudeSession) syncPermissionModeFromStatus(raw map[string]any) {
	mode, _ := raw["permissionMode"].(string)
	if mode == "" {
		return
	}
	cs.permissionMode.Store(mode)
}
