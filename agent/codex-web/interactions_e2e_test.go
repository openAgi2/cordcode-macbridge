package codexweb

// interactions_e2e_test.go —— Phase 4 真实官方 app-server 交互回归。
// CODEXWEB_E2E=1 时使用隔离 CODEX_HOME + 本地 mock Responses provider；mock 只
// 控制上游模型输出，审批/requestUserInput server request 与 response 均走官方 wire。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func e2eStartInteractionThread(t *testing.T, cl *Client, cwd string, extra map[string]any) string {
	t.Helper()
	params := map[string]any{"cwd": cwd, "model": "mock-model", "modelProvider": "mockpi"}
	for k, v := range extra {
		params[k] = v
	}
	raw, rpcErr, err := cl.RequestContext(context.Background(), "thread/start", params)
	if err != nil || rpcErr != nil {
		t.Fatalf("thread/start: %v / %v", err, rpcErr)
	}
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Thread.ID == "" {
		t.Fatalf("thread/start decode: %v (%s)", err, raw)
	}
	return result.Thread.ID
}

func e2eStartInteractionTurn(t *testing.T, cl *Client, threadID, prompt string) string {
	t.Helper()
	raw, rpcErr, err := cl.RequestContext(context.Background(), "turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	})
	if err != nil || rpcErr != nil {
		t.Fatalf("turn/start: %v / %v", err, rpcErr)
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Turn.ID == "" {
		t.Fatalf("turn/start decode: %v (%s)", err, raw)
	}
	return result.Turn.ID
}

func e2eWaitServerRequest(t *testing.T, cl *Client, method, threadID string, timeout time.Duration) ServerRequest {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case sr, ok := <-cl.ServerRequests():
			if !ok {
				t.Fatal("server request channel closed")
			}
			if sr.Method == method && sr.ThreadID == threadID {
				return sr
			}
		case <-deadline:
			t.Fatalf("%s 内未收到 %s", timeout, method)
		}
	}
}

func e2eWaitServerRequestForTurn(t *testing.T, cl *Client, method, threadID, turnID string, timeout time.Duration) ServerRequest {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case sr, ok := <-cl.ServerRequests():
			if !ok {
				t.Fatal("server request channel closed")
			}
			t.Logf("official server request while waiting: %s", sr.Method)
			if sr.Method == method && sr.ThreadID == threadID {
				return sr
			}
		case n, ok := <-cl.Notifications():
			if !ok {
				t.Fatal("notification channel closed")
			}
			var ids struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
				Turn     struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"turn"`
				Item struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				} `json:"item"`
			}
			_ = json.Unmarshal(n.Params, &ids)
			if ids.ThreadID == threadID {
				t.Logf("official notification while waiting: method=%s turnId=%s turn.id=%s status=%s item.type=%s", n.Method, ids.TurnID, ids.Turn.ID, ids.Turn.Status, ids.Item.Type)
			}
			if n.Method == "warning" {
				t.Logf("official warning params: %s", n.Params)
			}
			if n.Method == "turn/completed" && ids.ThreadID == threadID && ids.Turn.ID == turnID {
				t.Fatalf("turn completed before %s: status=%s", method, ids.Turn.Status)
			}
		case <-deadline:
			t.Fatalf("%s 内未收到 %s", timeout, method)
		}
	}
}

func e2eWaitTurnStatus(t *testing.T, cl *Client, threadID, turnID string, timeout time.Duration) string {
	t.Helper()
	var status string
	ok := waitNotification(t, cl, timeout, "turn/completed", func(params json.RawMessage) bool {
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turn"`
		}
		if json.Unmarshal(params, &p) != nil || p.ThreadID != threadID || p.Turn.ID != turnID {
			return false
		}
		status = p.Turn.Status
		return true
	})
	if !ok {
		t.Fatalf("%s 内未收到 turn/completed(%s)", timeout, turnID)
	}
	return status
}

func TestE2EInteractionApprovals(t *testing.T) {
	if !e2eEnabled(t) {
		return
	}
	providerURL := e2eMockProvider(t)
	_, home, workDir := e2eSetupBase(t, providerURL, nil)
	ep, err := Probe(ProbeOptions{CodexHome: home, WorkDir: workDir})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer func() { _ = ep.Close() }()
	cl := ep.Client()
	a := New(nil)
	a.endpoint = ep

	// command approval allow：官方 request → adapter accept → resolved → completed。
	allowThread := e2eStartInteractionThread(t, cl, workDir, map[string]any{"approvalPolicy": "untrusted"})
	allowTurn := e2eStartInteractionTurn(t, cl, allowThread, "MOCK:CMD:echo codexweb-e2e-allow")
	allowReq := e2eWaitServerRequest(t, cl, "item/commandExecution/requestApproval", allowThread, 90*time.Second)
	allowEvents := a.handleServerRequest(allowReq)
	if len(allowEvents) != 1 || allowEvents[0].Type != core.EventPermissionRequest {
		t.Fatalf("allow approval event = %+v", allowEvents)
	}
	if err := a.RespondSessionPermission(context.Background(), allowThread, allowEvents[0].RequestID, core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("allow response: %v", err)
	}
	if !waitNotification(t, cl, 30*time.Second, "serverRequest/resolved", nil) {
		t.Fatal("allow response missing serverRequest/resolved")
	}
	if status := e2eWaitTurnStatus(t, cl, allowThread, allowTurn, 90*time.Second); status != "completed" {
		t.Fatalf("allow turn status = %q", status)
	}

	// command approval deny：官方 cancel vocabulary 必须收口 interrupted。
	denyThread := e2eStartInteractionThread(t, cl, workDir, map[string]any{"approvalPolicy": "untrusted"})
	denyTurn := e2eStartInteractionTurn(t, cl, denyThread, "MOCK:CMD:echo codexweb-e2e-deny")
	denyReq := e2eWaitServerRequest(t, cl, "item/commandExecution/requestApproval", denyThread, 90*time.Second)
	denyEvents := a.handleServerRequest(denyReq)
	if err := a.RespondSessionPermission(context.Background(), denyThread, denyEvents[0].RequestID, core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatalf("deny response: %v", err)
	}
	if !waitNotification(t, cl, 30*time.Second, "serverRequest/resolved", nil) {
		t.Fatal("deny response missing serverRequest/resolved")
	}
	if status := e2eWaitTurnStatus(t, cl, denyThread, denyTurn, 90*time.Second); status != "interrupted" {
		t.Fatalf("deny turn status = %q, want interrupted", status)
	}
}

func TestE2EInteractionUserInput(t *testing.T) {
	if !e2eEnabled(t) {
		return
	}
	providerURL := e2eMockProvider(t)
	_, home, workDir := e2eSetupBase(t, providerURL, nil)
	// requestUserInput 是逐项版本门控的 experimental surface；真实 initialize
	// 必须声明 experimentalApi，不能因测试脚手架漏 capability 得出假失败。
	ep, err := Probe(ProbeOptions{CodexHome: home, WorkDir: workDir, ExperimentalAPI: true})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer func() { _ = ep.Close() }()
	cl := ep.Client()
	a := New(nil)
	a.endpoint = ep

	// requestUserInput 三题批：option/Other 自由文本/option 映射回官方 answers map 后 turn 继续。
	askThread := e2eStartInteractionThread(t, cl, workDir, map[string]any{
		"config": map[string]any{"features.default_mode_request_user_input": true},
	})
	askTurn := e2eStartInteractionTurn(t, cl, askThread, "MOCK:ASK3 multi question")
	askReq := e2eWaitServerRequestForTurn(t, cl, "item/tool/requestUserInput", askThread, askTurn, 30*time.Second)
	askEvents := a.handleServerRequest(askReq)
	if len(askEvents) != 1 || askEvents[0].UserInput == nil || len(askEvents[0].UserInput.Questions) != 3 {
		t.Fatalf("ask3 event = %+v", askEvents)
	}
	ui := askEvents[0].UserInput
	answers := []core.UserInputAnswer{
		{QuestionID: ui.Questions[0].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[0].Options[0].ID}}},
		{QuestionID: ui.Questions[1].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "e2e note"}}},
		{QuestionID: ui.Questions[2].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[2].Options[0].ID}}},
	}
	resolution, err := a.ResolveUserInput(context.Background(), ui.InteractionID, "e2e-action", core.UserInputActionAnswer, answers)
	if err != nil || resolution.Outcome != core.UserInputOutcomeAccepted {
		t.Fatalf("ask3 resolve = %+v / %v", resolution, err)
	}
	if !waitNotification(t, cl, 30*time.Second, "serverRequest/resolved", nil) {
		t.Fatal("ask3 response missing serverRequest/resolved")
	}
	if status := e2eWaitTurnStatus(t, cl, askThread, askTurn, 90*time.Second); status != "completed" {
		t.Fatalf("ask3 turn status = %q", status)
	}
}
