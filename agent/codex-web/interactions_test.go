package codexweb

// interactions_test.go —— Phase 4 审批与 requestUserInput contract tests。
// server request 输入来自 Phase 0 官方 raw fixture；断言只覆盖已采样 wire shape。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func interactionTestAgent(t *testing.T, epoch ConnectionEpoch) (*Agent, *scriptedTransport, *Client) {
	t.Helper()
	s := newScripted()
	cl := NewClient(s, epoch)
	t.Cleanup(func() { _ = cl.Close() })
	a := New(nil)
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "test"}
	ep.client = cl
	a.endpoint = ep
	return a, s, cl
}

func officialInteractionRequest(t *testing.T, method string, occurrence int, epoch ConnectionEpoch) ServerRequest {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "official-0.149.0-alpha.4", "dumps", "interaction", "raw.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row struct {
			Dir string `json:"dir"`
			Msg struct {
				ID     json.Number     `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			} `json:"msg"`
		}
		dec := json.NewDecoder(strings.NewReader(scanner.Text()))
		dec.UseNumber()
		if dec.Decode(&row) != nil || row.Dir != "server" || row.Msg.Method != method || row.Msg.ID == "" {
			continue
		}
		if occurrence > 0 {
			occurrence--
			continue
		}
		var identity struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		if err := json.Unmarshal(row.Msg.Params, &identity); err != nil {
			t.Fatal(err)
		}
		return ServerRequest{Epoch: epoch, RequestID: row.Msg.ID, ThreadID: identity.ThreadID, TurnID: identity.TurnID, Method: method, Params: row.Msg.Params}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("official interaction request not found: %s[%d]", method, occurrence)
	return ServerRequest{}
}

type serverResponseFrame struct {
	ID     json.Number     `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int64  `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func lastServerResponse(t *testing.T, s *scriptedTransport) serverResponseFrame {
	t.Helper()
	frames := s.sentFrames()
	if len(frames) == 0 {
		t.Fatal("expected a server response frame")
	}
	var got serverResponseFrame
	dec := json.NewDecoder(strings.NewReader(frames[len(frames)-1]))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func decodeResultMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var got map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestApprovalOfficialRequestsAndDecisionVocabulary(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		occurrence int
		behavior   string
		tool       string
		want       map[string]any
	}{
		{"command-accept", "item/commandExecution/requestApproval", 0, "allow", "Bash", map[string]any{"decision": "accept"}},
		{"command-cancel", "item/commandExecution/requestApproval", 1, "deny", "Bash", map[string]any{"decision": "cancel"}},
		{"file-cancel", "item/fileChange/requestApproval", 0, "deny", "Patch", map[string]any{"decision": "cancel"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, transport, _ := interactionTestAgent(t, 7)
			sr := officialInteractionRequest(t, tt.method, tt.occurrence, 7)
			events := a.handleServerRequest(sr)
			if len(events) != 1 || events[0].Type != core.EventPermissionRequest || events[0].ToolName != tt.tool {
				t.Fatalf("approval event mismatch: %+v", events)
			}
			if !reflect.DeepEqual(events[0].PermissionActions, []string{"approve", "reject"}) {
				t.Fatalf("Codex Web must not advertise unsupported always actions: %v", events[0].PermissionActions)
			}
			wantID := interactionID(sr.ThreadID, events[0].ItemID)
			if events[0].RequestID != wantID {
				t.Fatalf("interaction id = %q, want %q", events[0].RequestID, wantID)
			}
			if err := a.RespondSessionPermission(context.Background(), sr.ThreadID, wantID, core.PermissionResult{Behavior: tt.behavior}); err != nil {
				t.Fatal(err)
			}
			frame := lastServerResponse(t, transport)
			if frame.ID != sr.RequestID || !reflect.DeepEqual(decodeResultMap(t, frame.Result), tt.want) {
				t.Fatalf("response = id:%s result:%s, want id:%s result:%v", frame.ID, frame.Result, sr.RequestID, tt.want)
			}
		})
	}
}

func TestPermissionApprovalEchoAndDeny(t *testing.T) {
	for _, behavior := range []string{"allow", "deny"} {
		t.Run(behavior, func(t *testing.T) {
			a, transport, _ := interactionTestAgent(t, 9)
			sr := officialInteractionRequest(t, "item/permissions/requestApproval", 0, 9)
			events := a.handleServerRequest(sr)
			if len(events) != 1 || events[0].ToolName != "Permissions" {
				t.Fatalf("permission event mismatch: %+v", events)
			}
			if err := a.RespondSessionPermission(context.Background(), sr.ThreadID, events[0].RequestID, core.PermissionResult{Behavior: behavior}); err != nil {
				t.Fatal(err)
			}
			got := decodeResultMap(t, lastServerResponse(t, transport).Result)
			if got["scope"] != "session" {
				t.Fatalf("scope = %#v", got["scope"])
			}
			permissions, ok := got["permissions"].(map[string]any)
			if !ok {
				t.Fatalf("permissions shape = %#v", got["permissions"])
			}
			if behavior == "allow" && len(permissions) == 0 {
				t.Fatal("allow must echo official requested permissions")
			}
			if behavior == "deny" && len(permissions) != 0 {
				t.Fatalf("deny permissions = %#v, want empty object", permissions)
			}
		})
	}
}

func TestApprovalResolutionIsIdempotentAcrossSignals(t *testing.T) {
	a, _, _ := interactionTestAgent(t, 3)
	sr := officialInteractionRequest(t, "item/commandExecution/requestApproval", 0, 3)
	events := a.handleServerRequest(sr)
	id := events[0].RequestID
	resolvedParams, _ := json.Marshal(map[string]any{"threadId": sr.ThreadID, "requestId": sr.RequestID})
	first := a.resolvedEvents(Notification{Epoch: 3, Method: "serverRequest/resolved", Params: resolvedParams})
	second := a.itemCompletedResolution(sr.ThreadID, events[0].ItemID)
	third := a.resolvedEvents(Notification{Epoch: 3, Method: "serverRequest/resolved", Params: resolvedParams})
	if len(first) != 1 || first[0].RequestID != id || len(second) != 0 || len(third) != 0 {
		t.Fatalf("resolution events = first:%+v second:%+v third:%+v", first, second, third)
	}
}

func TestApprovalDuplicateSubmissionWritesOnce(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 6)
	sr := officialInteractionRequest(t, "item/commandExecution/requestApproval", 0, 6)
	id := a.handleServerRequest(sr)[0].RequestID
	if err := a.RespondSessionPermission(context.Background(), sr.ThreadID, id, core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatal(err)
	}
	if err := a.RespondSessionPermission(context.Background(), sr.ThreadID, id, core.PermissionResult{Behavior: "deny"}); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("duplicate response error = %v", err)
	}
	if len(transport.sentFrames()) != 1 {
		t.Fatalf("duplicate response wrote %d frames: %v", len(transport.sentFrames()), transport.sentFrames())
	}
}

func TestApprovalUnknownBehaviorFailsClosed(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 6)
	sr := officialInteractionRequest(t, "item/fileChange/requestApproval", 0, 6)
	id := a.handleServerRequest(sr)[0].RequestID
	if err := a.RespondSessionPermission(context.Background(), sr.ThreadID, id, core.PermissionResult{Behavior: "ask"}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown behavior error = %v", err)
	}
	if len(transport.sentFrames()) != 0 {
		t.Fatalf("unknown behavior must not write: %v", transport.sentFrames())
	}
}

func TestApprovalEpochChangeAndDropRejectResponse(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 2)
	sr := officialInteractionRequest(t, "item/fileChange/requestApproval", 0, 1)
	events := a.handleServerRequest(sr)
	if err := a.RespondSessionPermission(context.Background(), sr.ThreadID, events[0].RequestID, core.PermissionResult{Behavior: "allow"}); err == nil || !strings.Contains(err.Error(), "epoch changed") {
		t.Fatalf("stale epoch error = %v", err)
	}
	if len(transport.sentFrames()) != 0 {
		t.Fatalf("stale epoch must not write: %v", transport.sentFrames())
	}
	if dropped := a.registry.DropEpoch(1); dropped != 1 || a.registry.Lookup(events[0].RequestID) != nil {
		t.Fatalf("DropEpoch = %d, pending=%v", dropped, a.registry.Lookup(events[0].RequestID))
	}
}

func TestApprovalMissingIdentityReturnsInvalidParams(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 4)
	sr := ServerRequest{Epoch: 4, RequestID: "17", Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"th","turnId":"tu","command":"echo x"}`)}
	if events := a.handleServerRequest(sr); len(events) != 0 {
		t.Fatalf("missing item identity must not surface: %+v", events)
	}
	frame := lastServerResponse(t, transport)
	if frame.ID != "17" || frame.Error == nil || frame.Error.Code != -32602 {
		t.Fatalf("error frame = %+v", frame)
	}
}

func TestUserInputOfficialFixtureNormalizationAndWireAnswer(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 11)
	sr := officialInteractionRequest(t, "item/tool/requestUserInput", 0, 11)
	events := a.handleServerRequest(sr)
	if len(events) != 1 || events[0].UserInput == nil {
		t.Fatalf("user input event = %+v", events)
	}
	ui := events[0].UserInput
	if ui.InteractionID != interactionID(sr.ThreadID, events[0].ItemID) || ui.Status != core.UserInputStatusPending || !ui.CanRespond || !ui.CanReject || len(ui.Questions) != 1 {
		t.Fatalf("normalized interaction = %+v", ui)
	}
	q := ui.Questions[0]
	if q.ID != "confirm_path" || q.AnswerMode != core.UserInputAnswerModeSingle || !q.AllowsCustomAnswer || len(q.Options) != 2 || q.Options[0].ID != "confirm_path_o_0" {
		t.Fatalf("normalized question = %+v", q)
	}
	resolution, err := a.ResolveUserInput(context.Background(), ui.InteractionID, "act-1", core.UserInputActionAnswer, []core.UserInputAnswer{{
		QuestionID: q.ID,
		Values:     []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: q.Options[0].ID}},
	}})
	if err != nil || resolution.Outcome != core.UserInputOutcomeAccepted {
		t.Fatalf("resolution = %+v, %v", resolution, err)
	}
	frame := lastServerResponse(t, transport)
	if frame.ID != sr.RequestID || string(frame.Result) != `{"answers":{"confirm_path":{"answers":["Yes (Recommended)"]}}}` {
		t.Fatalf("wire response = id:%s result:%s", frame.ID, frame.Result)
	}
	again, err := a.ResolveUserInput(context.Background(), ui.InteractionID, "act-2", core.UserInputActionAnswer, []core.UserInputAnswer{{
		QuestionID: q.ID,
		Values:     []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: q.Options[0].ID}},
	}})
	if err != nil || again.Outcome != core.UserInputOutcomeInProgress {
		t.Fatalf("before official resolved should stay in-progress: %+v, %v", again, err)
	}
	// 收口统一由官方 serverRequest/resolved 驱动（2026-08-25：提前本地收口会让观察
	// 面板永远不消失）；官方 resolved 后再提交才是 already_resolved。
	resolved := a.resolvedEvents(Notification{Epoch: 11, Method: "serverRequest/resolved", Params: json.RawMessage(`{"threadId":"` + sr.ThreadID + `","requestId":` + string(sr.RequestID) + `}`)})
	if len(resolved) != 1 || resolved[0].Type != core.EventUserInputResolved {
		t.Fatalf("resolved events = %+v, want user_input_resolved", resolved)
	}
	third, err := a.ResolveUserInput(context.Background(), ui.InteractionID, "act-3", core.UserInputActionAnswer, nil)
	if err != nil || third.Outcome != core.UserInputOutcomeAlreadyResolved || len(transport.sentFrames()) != 1 {
		t.Fatalf("idempotent resolve = %+v, %v, frames=%v", third, err, transport.sentFrames())
	}
}

func TestUserInputInvalidBackendRequestIsNotRegistered(t *testing.T) {
	for _, questions := range []string{
		`[]`,
		`[{"id":"","question":"Q?"}]`,
		`[{"id":"q","question":"Q?","options":[]}]`,
		`[{"id":"q","question":"Q?","options":[{"label":"   "},{"label":"Two"}]}]`,
		`[{"id":"q","question":"Q?","options":[{"label":"1"},{"label":"2"}]},{"id":"q","question":"Again?","options":[{"label":"1"},{"label":"2"}]}]`,
	} {
		a, transport, _ := interactionTestAgent(t, 5)
		params := json.RawMessage(`{"threadId":"th","turnId":"tu","itemId":"it","questions":` + questions + `}`)
		events := a.handleServerRequest(ServerRequest{Epoch: 5, RequestID: "8", ThreadID: "th", TurnID: "tu", Method: "item/tool/requestUserInput", Params: params})
		if len(events) != 1 || events[0].UserInput == nil || events[0].UserInput.Status != core.UserInputStatusFailed || events[0].UserInput.DiagnosticCode != "invalid_backend_request" {
			t.Fatalf("failed event = %+v", events)
		}
		if a.registry.Lookup("th:it") != nil || len(transport.sentFrames()) != 0 {
			t.Fatalf("invalid request registered or written: pending=%v frames=%v", a.registry.Lookup("th:it"), transport.sentFrames())
		}
	}
}

func TestUserInputPreservesOfficialQuestionID(t *testing.T) {
	raw := []userInputRawQuestion{{ID: " q ", Question: "Q?", Options: []struct {
		Label       string  `json:"label"`
		Description *string `json:"description"`
	}{{Label: "One"}, {Label: "Two"}}}}
	snap, err := normalizeUserInputQuestions(raw)
	if err != nil {
		t.Fatal(err)
	}
	questions := normalizedUserInputQuestions("ignored", raw)
	if !reflect.DeepEqual(snap.Order, []string{" q "}) || len(questions) != 1 || questions[0].ID != " q " {
		t.Fatalf("official id was rewritten: order=%q questions=%+v", snap.Order, questions)
	}
}

func TestUserInputMultiQuestionTextAndLegacyAlias(t *testing.T) {
	t.Run("multi-question", func(t *testing.T) {
		a, transport, _ := interactionTestAgent(t, 13)
		params := json.RawMessage(`{"threadId":"th","turnId":"tu","itemId":"it","questions":[{"id":"choice","question":"Pick","isOther":false,"options":[{"label":"One","description":"first"},{"label":"Two"}]},{"id":"note","question":"Why?","isOther":true,"options":[{"label":"No note"},{"label":"Add note"}]}]}`)
		events := a.handleServerRequest(ServerRequest{Epoch: 13, RequestID: "21", ThreadID: "th", TurnID: "tu", Method: "item/tool/requestUserInput", Params: params})
		ui := events[0].UserInput
		if len(ui.Questions) != 2 || ui.Questions[1].AnswerMode != core.UserInputAnswerModeSingle || !ui.Questions[1].AllowsCustomAnswer {
			t.Fatalf("questions = %+v", ui.Questions)
		}
		_, err := a.ResolveUserInput(context.Background(), ui.InteractionID, "multi", core.UserInputActionAnswer, []core.UserInputAnswer{
			{QuestionID: "choice", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "choice_o_0"}}},
			{QuestionID: "note", Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "  keep original spacing  "}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := decodeResultMap(t, lastServerResponse(t, transport).Result)
		want := map[string]any{
			"answers": map[string]any{
				"choice": map[string]any{"answers": []any{"One"}},
				"note":   map[string]any{"answers": []any{"  keep original spacing  "}},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("wire answers = %#v, want %#v", got, want)
		}
	})

	t.Run("legacy-option-alias", func(t *testing.T) {
		a, transport, _ := interactionTestAgent(t, 14)
		sr := officialInteractionRequest(t, "item/tool/requestUserInput", 0, 14)
		ui := a.handleServerRequest(sr)[0].UserInput
		if err := a.respondUserInput(context.Background(), ui.InteractionID, []string{"confirm_path_o_1"}, false); err != nil {
			t.Fatal(err)
		}
		got := decodeResultMap(t, lastServerResponse(t, transport).Result)
		want := map[string]any{"answers": map[string]any{"confirm_path": map[string]any{"answers": []any{"No"}}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("legacy wire answer = %#v, want %#v", got, want)
		}
	})
}

func TestUserInputAnswerValidationAndRejectFailClosed(t *testing.T) {
	snap := &userInputSnapshot{
		OptionLabel: map[string]string{"choice_o_0": "Yes"},
		Mode:        map[string]core.UserInputAnswerMode{"choice": core.UserInputAnswerModeSingle, "note": core.UserInputAnswerModeSingle},
		Custom:      map[string]bool{"choice": false, "note": true},
		Order:       []string{"choice", "note"},
	}
	invalid := []struct {
		name    string
		answers []core.UserInputAnswer
	}{
		{"missing-required", []core.UserInputAnswer{{QuestionID: "choice", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "choice_o_0"}}}}},
		{"unknown-question", []core.UserInputAnswer{{QuestionID: "other"}}},
		{"unknown-option", []core.UserInputAnswer{{QuestionID: "choice", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "bad"}}}, {QuestionID: "note", Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "ok"}}}}},
		{"custom-not-allowed", []core.UserInputAnswer{{QuestionID: "choice", Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "other"}}}, {QuestionID: "note", Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "ok"}}}}},
		{"empty-text", []core.UserInputAnswer{{QuestionID: "choice", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "choice_o_0"}}}, {QuestionID: "note", Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "  "}}}}},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildUserInputWireAnswers(snap, tt.answers)
			var coded *core.UserInputError
			if err == nil || !asUserInputError(err, &coded) || coded.Code != "invalid_answer_shape" {
				t.Fatalf("error = %#v", err)
			}
		})
	}

	a, transport, _ := interactionTestAgent(t, 12)
	sr := officialInteractionRequest(t, "item/tool/requestUserInput", 0, 12)
	ui := a.handleServerRequest(sr)[0].UserInput
	// 跳过 = 官方空 answers 响应（Mac 面板「跳过」语义），turn 由官方继续。
	resolution, err := a.ResolveUserInput(context.Background(), ui.InteractionID, "reject", core.UserInputActionReject, nil)
	if err != nil || resolution.CurrentStatus != core.UserInputStatusRejected {
		t.Fatalf("skip resolution = %+v, %v", resolution, err)
	}
	frame := lastServerResponse(t, transport)
	if frame.ID != sr.RequestID || string(frame.Result) != `{"answers":{}}` {
		t.Fatalf("skip wire response = id:%s result:%s", frame.ID, frame.Result)
	}
	// 收口由官方 resolved 驱动：跳过 → user_input_resolved status=rejected（iOS 显示「已跳过」）。
	evs := a.resolvedEvents(Notification{Epoch: 12, Method: "serverRequest/resolved", Params: json.RawMessage(`{"threadId":"` + sr.ThreadID + `","requestId":` + string(sr.RequestID) + `}`)})
	if len(evs) != 1 || evs[0].UserInput == nil || evs[0].UserInput.Status != core.UserInputStatusRejected {
		t.Fatalf("skip resolved events = %+v", evs)
	}
}

func asUserInputError(err error, target **core.UserInputError) bool {
	if err == nil {
		return false
	}
	got, ok := err.(*core.UserInputError)
	if ok {
		*target = got
	}
	return ok
}

// TestApprovalEventReasonSeparateFromCommand：官方 reason 必须单独走 Content
// （wire permission_request.reason → 投影 part.Title），不得拼进 ToolInput——
// 拼进后 iOS 会把「命令+文案」混成权限卡标题（2026-08-25 真机只显示命令前两行）。
func TestApprovalEventReasonSeparateFromCommand(t *testing.T) {
	a, _, _ := interactionTestAgent(t, 11)
	const reason = "需要在 /Users/jacklee/Projects/Chat/红楼梦故事.txt（工作区外路径）末尾追加一段故事，是否允许修改该文件？"
	const command = `/bin/zsh -lc "python3 - <<'PYEOF'
# -*- coding: utf-8 -*-
path = \"/Users/jacklee/Projects/Chat/红楼梦故事.txt\"
PYEOF"`
	sr := ServerRequest{
		Epoch: 11, RequestID: "5", ThreadID: "th-1", TurnID: "tu-1",
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(fmt.Sprintf(`{"threadId":"th-1","turnId":"tu-1","itemId":"call-1","command":%s,"reason":%s}`,
			jsonQuote(command), jsonQuote(reason))),
	}
	events := a.handleServerRequest(sr)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Content != reason {
		t.Fatalf("Content = %q, want official reason", ev.Content)
	}
	if ev.ToolInput != command {
		t.Fatalf("ToolInput = %q, want pure command", ev.ToolInput)
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestResolvedEventsUserInputEmitsUserInputResolved：官方 serverRequest/resolved
// 对 requestUserInput 交互按 kind 产出 user_input_resolved（投影把面板收为
// answered）而不是 permission_resolved；幂等（二次 resolved 不再发）。
func TestResolvedEventsUserInputEmitsUserInputResolved(t *testing.T) {
	a, _, _ := interactionTestAgent(t, 12)
	a.registry.Register(&Interaction{
		InteractionID: "th-u2:call-x",
		Kind:          InteractionUserInput,
		Epoch:         12,
		RequestID:     "31",
		ThreadID:      "th-u2",
		TurnID:        "tu-2",
		ItemID:        "call-x",
		Method:        "item/tool/requestUserInput",
	})
	params := json.RawMessage(`{"threadId":"th-u2","requestId":31}`)
	events := a.resolvedEvents(Notification{Epoch: 12, Method: "serverRequest/resolved", Params: params})
	if len(events) != 1 || events[0].Type != core.EventUserInputResolved {
		t.Fatalf("events = %+v, want user_input_resolved", events)
	}
	ui := events[0].UserInput
	if ui == nil || ui.InteractionID != "th-u2:call-x" || ui.Status != core.UserInputStatusAnswered {
		t.Fatalf("user input resolution = %+v", ui)
	}
	if pending := a.registry.Lookup("th-u2:call-x"); pending != nil {
		t.Fatalf("interaction still pending after resolved: %+v", pending)
	}
	if again := a.resolvedEvents(Notification{Epoch: 12, Method: "serverRequest/resolved", Params: params}); len(again) != 0 {
		t.Fatalf("duplicate resolved emitted events: %+v", again)
	}
}

// TestUserInputFourOptionsRegistersAndAnswers：官方仅要求每题 options 非空，
// 数量无上限（request_user_input_spec.rs）；2026-08-25 iOS 发起真机模型生成
// 4 选项曾被 2-3 硬限误判 invalid_backend_request。4 选项必须正常注册、
// 应答映射到官方 label。
func TestUserInputFourOptionsRegistersAndAnswers(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 13)
	params := json.RawMessage(`{"threadId":"th","turnId":"tu","itemId":"it4","questions":[{"id":"pick","question":"选一个","options":[{"label":"甲"},{"label":"乙"},{"label":"丙"},{"label":"丁"}]}]}`)
	events := a.handleServerRequest(ServerRequest{Epoch: 13, RequestID: "9", ThreadID: "th", TurnID: "tu", Method: "item/tool/requestUserInput", Params: params})
	if len(events) != 1 || events[0].UserInput == nil || events[0].UserInput.Status != core.UserInputStatusPending {
		t.Fatalf("4-option event = %+v", events)
	}
	if len(events[0].UserInput.Questions[0].Options) != 4 {
		t.Fatalf("options = %d, want 4", len(events[0].UserInput.Questions[0].Options))
	}
	if a.registry.Lookup("th:it4") == nil {
		t.Fatal("4-option interaction not registered")
	}
	resolution, err := a.ResolveUserInput(context.Background(), "th:it4", "act-4", core.UserInputActionAnswer, []core.UserInputAnswer{{
		QuestionID: "pick",
		Values:     []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "pick_o_3"}},
	}})
	if err != nil || resolution.Outcome != core.UserInputOutcomeAccepted {
		t.Fatalf("4-option resolve = %+v, %v", resolution, err)
	}
	frame := lastServerResponse(t, transport)
	if frame.ID != "9" || string(frame.Result) != `{"answers":{"pick":{"answers":["丁"]}}}` {
		t.Fatalf("4-option wire response = id:%s result:%s", frame.ID, frame.Result)
	}
}
