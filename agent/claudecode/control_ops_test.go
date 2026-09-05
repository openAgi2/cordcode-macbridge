package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// control_ops_test.go —— Phase 2 会话内控制操作定向测试。
// fixture 纪律同 Phase 1：真实 control_response 原文（Phase 0 dump）驱动。

// pipeSession 建一个无进程的最小 claudeSession（stdin=pipe），reader 侧解码
// 信封并按 script 回帧。
func pipeSession(t *testing.T, respond func(env map[string]any) map[string]any) (*claudeSession, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Skipf("pipe: %v", err)
	}
	cs := &claudeSession{stdin: w, done: make(chan struct{})}
	cs.alive.Store(true)
	go func() {
		var env map[string]any
		if err := json.NewDecoder(r).Decode(&env); err != nil {
			return
		}
		if frame := respond(env); frame != nil {
			cs.dispatchControlResponse(frame)
		}
	}()
	return cs, r
}

func ctrlFrame(rid string, inner map[string]any) map[string]any {
	return map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": rid,
			"response":   inner,
		},
	}
}

func errFrame(rid, message string) map[string]any {
	return map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": rid,
			"message":    message,
		},
	}
}

func TestSetModelLive_SendsOfficialEnvelope(t *testing.T) {
	var gotInner map[string]any
	cs, r := pipeSession(t, func(env map[string]any) map[string]any {
		inner, _ := env["request"].(map[string]any)
		gotInner = inner
		rid, _ := env["request_id"].(string)
		return ctrlFrame(rid, nil)
	})
	defer r.Close()

	if err := cs.SetModelLive(context.Background(), "sonnet"); err != nil {
		t.Fatalf("SetModelLive: %v", err)
	}
	if gotInner["subtype"] != "set_model" || gotInner["model"] != "sonnet" {
		t.Fatalf("envelope inner = %v", gotInner)
	}
	if cs.model != "sonnet" {
		t.Errorf("session model not updated: %q", cs.model)
	}
}

// get_context_usage（升 A，2026-09-05）：真样本 fixture 驱动——CLI 2.1.261
// dump（scripts/claudecode-phase0/dumps/ctx.jsonl req_x2，detail=summary）。
func TestGetContextUsageLive_RealPayload(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "context-usage", "get_context_usage-summary-2.1.261.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("fixture JSON: %v", err)
	}
	var gotInner map[string]any
	cs, r := pipeSession(t, func(env map[string]any) map[string]any {
		inner, _ := env["request"].(map[string]any)
		gotInner = inner
		rid, _ := env["request_id"].(string)
		return ctrlFrame(rid, payload)
	})
	defer r.Close()

	usage := cs.GetContextUsageLive(context.Background())
	if usage == nil {
		t.Fatalf("usage = nil for real payload")
	}
	if gotInner["subtype"] != "get_context_usage" || gotInner["detail"] != "summary" {
		t.Fatalf("envelope inner = %v", gotInner)
	}
	// fixture 锚值（锁形状）：total 19626 / max 200000 / System prompt 2724 /
	// System tools 13989 / Messages 1（deferred 变体与未知分类不映射）
	if usage.UsedTokens != 19626 || usage.ContextWindow != 200000 {
		t.Fatalf("tokens = %d/%d", usage.UsedTokens, usage.ContextWindow)
	}
	if usage.SystemTokens != 2724 || usage.ToolsTokens != 13989 || usage.MessageTokens != 1 {
		t.Fatalf("breakdown = %d/%d/%d", usage.SystemTokens, usage.ToolsTokens, usage.MessageTokens)
	}
}

// fail closed：error 信封 / 未知形状（缺 totalTokens 或 maxTokens）→ nil，
// 调用方保持流帧 usage 语义。
func TestGetContextUsageLive_FailClosed(t *testing.T) {
	t.Run("error-envelope", func(t *testing.T) {
		cs, r := pipeSession(t, func(env map[string]any) map[string]any {
			rid, _ := env["request_id"].(string)
			return errFrame(rid, "boom")
		})
		defer r.Close()
		if cs.GetContextUsageLive(context.Background()) != nil {
			t.Fatalf("error envelope must yield nil")
		}
	})
	t.Run("unknown-shape", func(t *testing.T) {
		cs, r := pipeSession(t, func(env map[string]any) map[string]any {
			rid, _ := env["request_id"].(string)
			return ctrlFrame(rid, map[string]any{"unrelated": true})
		})
		defer r.Close()
		if cs.GetContextUsageLive(context.Background()) != nil {
			t.Fatalf("unknown shape must yield nil")
		}
	})
	t.Run("parser-guard", func(t *testing.T) {
		if occupancyFromContextUsagePayload(map[string]any{}) != nil {
			t.Fatalf("empty payload must yield nil")
		}
		if occupancyFromContextUsagePayload(map[string]any{"totalTokens": 10.0, "maxTokens": 0.0}) != nil {
			t.Fatalf("non-positive window must yield nil")
		}
	})
}

func TestSetModelLive_DefaultResets(t *testing.T) {
	cs, r := pipeSession(t, func(env map[string]any) map[string]any {
		rid, _ := env["request_id"].(string)
		return ctrlFrame(rid, nil)
	})
	defer r.Close()
	cs.model = "opus"

	if err := cs.SetModelLive(context.Background(), ""); err != nil {
		t.Fatalf("SetModelLive(default): %v", err)
	}
	if cs.model != "opus" {
		// 重置不改写本地记录的会话模型（官方语义：回到会话默认）
		t.Errorf("reset must not rewrite cs.model, got %q", cs.model)
	}
}

func TestSetModelLive_CLIErrorFailsVisibly(t *testing.T) {
	cs, r := pipeSession(t, func(env map[string]any) map[string]any {
		rid, _ := env["request_id"].(string)
		return errFrame(rid, "unknown model")
	})
	defer r.Close()

	err := cs.SetModelLive(context.Background(), "bogus-model")
	if err == nil {
		t.Fatalf("CLI rejection must surface as error")
	}
	if ce, ok := err.(*controlError); !ok || ce.message != "unknown model" {
		t.Errorf("error = %#v, want controlError with message", err)
	}
}

func TestSetPermissionModeLive_RestrictedSet(t *testing.T) {
	cs := &claudeSession{done: make(chan struct{})}
	for _, mode := range []string{"plan", "auto", "weird"} {
		if err := cs.SetPermissionModeLive(context.Background(), mode); err == nil {
			t.Errorf("mode %q must be rejected (restricted four-mode set)", mode)
		}
	}
}

func TestSetPermissionModeLive_SuccessSyncsLocalMode(t *testing.T) {
	var requested map[string]any
	cs, r := pipeSession(t, func(env map[string]any) map[string]any {
		requested, _ = env["request"].(map[string]any)
		rid, _ := env["request_id"].(string)
		return ctrlFrame(rid, map[string]any{"mode": "acceptEdits"})
	})
	defer r.Close()
	cs.setPermissionMode("default")

	if err := cs.SetPermissionModeLive(context.Background(), "acceptEdits"); err != nil {
		t.Fatalf("SetPermissionModeLive: %v", err)
	}
	if requested["mode"] != "acceptEdits" || requested["subtype"] != "set_permission_mode" {
		t.Fatalf("envelope inner = %v", requested)
	}
	if got, _ := cs.permissionMode.Load().(string); got != "acceptEdits" {
		t.Errorf("local mode not synced from echoed success body: %q", got)
	}
}

func TestCancelTurn_CapabilityGate(t *testing.T) {
	// 无 system/init（能力位未见）→ 不支持，回落既有 Close 路径。
	cs := &claudeSession{done: make(chan struct{})}
	cs.alive.Store(true)
	if err := cs.CancelTurn(context.Background()); err == nil {
		t.Fatalf("no interrupt_receipt_v1 capability must fail closed")
	}

	// 有能力位但 CLI 拒收 → controlError（同样回落）。
	cs2, r := pipeSession(t, func(env map[string]any) map[string]any {
		rid, _ := env["request_id"].(string)
		return errFrame(rid, "boom")
	})
	defer r.Close()
	cs2.alive.Store(true)
	cs2.storeInitCapabilities(map[string]any{"capabilities": []any{"interrupt_receipt_v1", "interrupt_cancel_queued_v1"}})
	if err := cs2.CancelTurn(context.Background()); err == nil {
		t.Fatalf("CLI error must surface")
	}
}

func TestCancelTurn_OfficialReceipt(t *testing.T) {
	var requested map[string]any
	cs, r := pipeSession(t, func(env map[string]any) map[string]any {
		requested, _ = env["request"].(map[string]any)
		rid, _ := env["request_id"].(string)
		return ctrlFrame(rid, map[string]any{"still_queued": []any{}, "cancelled": []any{}})
	})
	defer r.Close()
	cs.alive.Store(true)
	cs.storeInitCapabilities(map[string]any{"capabilities": []any{"interrupt_receipt_v1"}})

	if err := cs.CancelTurn(context.Background()); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	if requested["subtype"] != "interrupt" || requested["cancel_queued"] != true {
		t.Fatalf("envelope inner = %v (want cancel_queued:true)", requested)
	}
}

func TestStoreInitCapabilities_FromRealShape(t *testing.T) {
	cs := &claudeSession{}
	// Phase 0 dump turn.jsonl 的 system/init capabilities 原文形状
	cs.storeInitCapabilities(map[string]any{
		"capabilities": []any{"interrupt_receipt_v1", "interrupt_cancel_queued_v1", "msg_lifecycle_v1"},
	})
	for _, c := range []string{"interrupt_receipt_v1", "interrupt_cancel_queued_v1", "msg_lifecycle_v1"} {
		if !cs.hasCapability(c) {
			t.Errorf("capability %q missing", c)
		}
	}
	if cs.hasCapability("modelCatalog") {
		t.Errorf("modelCatalog must be absent (Phase 0 evidence)")
	}
	// 无 capabilities 字段的 system 帧（如 hook 帧）不覆盖既有集合
	cs.storeInitCapabilities(map[string]any{"hook_event": "Stop"})
	if !cs.hasCapability("interrupt_receipt_v1") {
		t.Errorf("absent field must not clear stored capabilities")
	}
}

func TestSyncPermissionModeFromStatus(t *testing.T) {
	cs := &claudeSession{}
	cs.setPermissionMode("default")
	cs.syncPermissionModeFromStatus(map[string]any{"subtype": "status", "permissionMode": "bypassPermissions"})
	if got, _ := cs.permissionMode.Load().(string); got != "bypassPermissions" {
		t.Errorf("mode = %q, want synced bypassPermissions", got)
	}
	cs.syncPermissionModeFromStatus(map[string]any{"subtype": "status"}) // 无字段：不动
	if got, _ := cs.permissionMode.Load().(string); got != "bypassPermissions" {
		t.Errorf("empty status must not clear mode, got %q", got)
	}
}

func TestControlOpTimeout_FailsClosed(t *testing.T) {
	// reader 不回帧：等待超时必须报错而不是挂死。
	r, w, err := os.Pipe()
	if err != nil {
		t.Skipf("pipe: %v", err)
	}
	defer r.Close()
	cs := &claudeSession{stdin: w, done: make(chan struct{})}
	cs.alive.Store(true)
	cs.storeInitCapabilities(map[string]any{"capabilities": []any{"interrupt_receipt_v1"}})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := cs.CancelTurn(ctx); err == nil {
		t.Fatalf("timeout must surface as error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("CancelTurn ignored its deadline")
	}
}
