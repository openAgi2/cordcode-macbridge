package codexremote

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func threadSettingsResult(mode, model string, effort any) map[string]any {
	return map[string]any{"threadSettings": map[string]any{
		"cwd": "/tmp", "approvalPolicy": "never", "approvalsReviewer": "user", "sandboxPolicy": map[string]any{"type": "readOnly"},
		"activePermissionProfile": nil, "model": model, "modelProvider": "openai", "serviceTier": nil, "effort": effort, "summary": nil,
		"collaborationMode": map[string]any{"mode": mode, "settings": map[string]any{
			"model": model, "reasoning_effort": effort, "developer_instructions": nil,
		}},
		"multiAgentMode": "explicitRequestOnly", "personality": nil,
	}}
}

func TestCollaborationModeGetHydratesAuthoritativeCurrentEpoch(t *testing.T) {
	rec := &recordedRPC{results: map[string]any{"thread/settings/get": threadSettingsResult("plan", "cold-model", "high")}}
	agent := newPlanReviewAgent(t, rec)
	agent.codec.ResetNativeSessionState()

	state, err := agent.GetSessionCollaborationMode(context.Background(), "thread_probe")
	if err != nil || state.Mode != "plan" || state.Model != "cold-model" || state.ReasoningEffort == nil || *state.ReasoningEffort != "high" {
		t.Fatalf("settings get = %+v err=%v", state, err)
	}
	call, ok := rec.last("thread/settings/get")
	if !ok || call.Params["threadId"] != "thread_probe" {
		t.Fatalf("settings get call = %+v ok=%v", call, ok)
	}
	current, ok := agent.CurrentSessionCollaborationMode("thread_probe")
	if !ok || current.Mode != "plan" || current.Model != "cold-model" {
		t.Fatalf("hydrated current = %+v ok=%v", current, ok)
	}
}

func TestCollaborationModeNewerNotificationWinsOverOlderGet(t *testing.T) {
	rec := &recordedRPC{results: map[string]any{"thread/settings/get": threadSettingsResult("default", "older-model", nil)}}
	agent := newPlanReviewAgent(t, rec)
	rec.hooks = map[string]func(){"thread/settings/get": func() {
		agent.codec.Decode(Notification{Method: "thread/settings/updated", Params: json.RawMessage(`{
			"threadId":"thread_probe","threadSettings":{"collaborationMode":{"mode":"plan","settings":{"model":"newer-model","reasoning_effort":"medium","developer_instructions":null}}}
		}`)})
	}}

	state, err := agent.GetSessionCollaborationMode(context.Background(), "thread_probe")
	if err != nil || state.Mode != "plan" || state.Model != "newer-model" || state.ReasoningEffort == nil || *state.ReasoningEffort != "medium" {
		t.Fatalf("converged settings = %+v err=%v", state, err)
	}
}

func TestCollaborationModeGetRejectsMissingOrUnsupportedSettings(t *testing.T) {
	for name, result := range map[string]any{
		"missing": map[string]any{},
		"unknown": threadSettingsResult("future", "model", nil),
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recordedRPC{results: map[string]any{"thread/settings/get": result}}
			agent := newPlanReviewAgent(t, rec)
			agent.codec.ResetNativeSessionState()
			if _, err := agent.GetSessionCollaborationMode(context.Background(), "thread_probe"); err == nil {
				t.Fatal("malformed authoritative settings must fail closed")
			}
		})
	}
}

func TestCollaborationModeAttachToleratesOnlyMissingReadMethodWithoutFabricatingState(t *testing.T) {
	for name, rpcErr := range map[string]*RPCError{
		"method-not-found": {
			Code:    -32601,
			Message: "Method not found",
		},
		"signed-runtime-unknown-variant": {
			Code:    -32600,
			Message: "Invalid request: unknown variant `thread/settings/get`, expected one of `initialize`",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recordedRPC{errors: map[string]*RPCError{"thread/settings/get": rpcErr}}
			agent := newPlanReviewAgent(t, rec)
			agent.codec.ResetNativeSessionState()

			if err := agent.refreshSessionCollaborationAfterAttach(context.Background(), agent.client, "thread_probe"); err != nil {
				t.Fatalf("legacy attach hydration = %v", err)
			}
			if current, ok := agent.CurrentSessionCollaborationMode("thread_probe"); ok {
				t.Fatalf("legacy attach fabricated collaboration state: %+v", current)
			}
			if _, err := agent.ListSessionCollaborationModes(context.Background(), "thread_probe"); err == nil || !strings.Contains(err.Error(), "authoritative") {
				t.Fatalf("legacy list must remain fail-closed, got %v", err)
			}
		})
	}
}

func TestCollaborationModeAttachRejectsOtherReadFailures(t *testing.T) {
	for name, rpcErr := range map[string]*RPCError{
		"invalid-params": {Code: -32602, Message: "invalid params"},
		"other-invalid-request": {
			Code:    -32600,
			Message: "Invalid request: unknown variant `thread/future/get`",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recordedRPC{errors: map[string]*RPCError{"thread/settings/get": rpcErr}}
			agent := newPlanReviewAgent(t, rec)
			agent.codec.ResetNativeSessionState()

			err := agent.refreshSessionCollaborationAfterAttach(context.Background(), agent.client, "thread_probe")
			got, ok := err.(*RPCError)
			if !ok || got.Code != rpcErr.Code {
				t.Fatalf("non-method-missing attach error = %#v", err)
			}
		})
	}
}

func TestCollaborationModeCatalogUsesOfficialPresetsAndNotificationState(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	catalog, err := agent.ListSessionCollaborationModes(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Presets) != 2 || catalog.Presets[0].Name != "Plan" || catalog.Presets[1].Name != "Default" {
		t.Fatalf("presets = %+v", catalog.Presets)
	}
	if catalog.Current.Model != "thread-model" || catalog.Current.Mode != "plan" {
		t.Fatalf("current = %+v", catalog.Current)
	}
}

func TestCollaborationModeUpdatePreservesThreadModelAndAppliesPresetMask(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	if err := agent.UpdateSessionCollaborationMode(context.Background(), "thread_probe", "Plan"); err != nil {
		t.Fatal(err)
	}
	call, ok := rec.last("thread/settings/update")
	if !ok {
		t.Fatalf("calls = %+v", rec.calls)
	}
	mode, _ := call.Params["collaborationMode"].(map[string]any)
	settings, _ := mode["settings"].(map[string]any)
	if mode["mode"] != "plan" || settings["model"] != "thread-model" || settings["reasoning_effort"] != "medium" {
		t.Fatalf("payload = %#v", call.Params)
	}
	if _, exists := settings["developer_instructions"]; !exists || settings["developer_instructions"] != nil {
		t.Fatalf("developer_instructions must be explicit null: %#v", settings)
	}
	if agent.defaultModel == settings["model"] {
		t.Fatal("global default model leaked into per-thread settings")
	}
	current, ok := agent.CurrentSessionCollaborationMode("thread_probe")
	if !ok || current.ReasoningEffort == nil || *current.ReasoningEffort != "high" {
		t.Fatalf("empty ACK must not optimistically replace notification state: %+v ok=%v", current, ok)
	}
}

func TestCollaborationModeFailsClosedWithoutAuthoritativeCurrentOrPreset(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	agent.codec.ResetNativeSessionState()
	if _, err := agent.ListSessionCollaborationModes(context.Background(), "thread_probe"); err == nil || !strings.Contains(err.Error(), "authoritative") {
		t.Fatalf("unknown current error = %v", err)
	}
	if err := agent.UpdateSessionCollaborationMode(context.Background(), "thread_probe", "Plan"); err == nil || !strings.Contains(err.Error(), "authoritative") {
		t.Fatalf("unknown current update error = %v", err)
	}
	effort := "high"
	agent.codec.applyCollaborationMode("thread_probe", core.SessionCollaborationMode{Mode: "default", Model: "thread-model", ReasoningEffort: &effort}, nil)
	if err := agent.UpdateSessionCollaborationMode(context.Background(), "thread_probe", "Missing"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("missing preset error = %v", err)
	}
}

func TestCollaborationModeUpdateRequiresStrictEmptyAck(t *testing.T) {
	rec := &recordedRPC{results: map[string]any{"thread/settings/update": map[string]any{"ok": true}}}
	agent := newPlanReviewAgent(t, rec)
	if err := agent.UpdateSessionCollaborationMode(context.Background(), "thread_probe", "Default"); err == nil || !strings.Contains(err.Error(), "unexpected fields") {
		t.Fatalf("strict ACK error = %v", err)
	}
}
