package grokbuild

import (
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestEncodeRequest(t *testing.T) {
	data, err := encodeRequest(1, "initialize", initializeParams{
		ProtocolVersion: 1,
		ClientInfo: &clientInfo{
			Name:    "test",
			Version: "1.0",
		},
	})
	if err != nil {
		t.Fatalf("encodeRequest: %v", err)
	}

	var req struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", req.JSONRPC)
	}
	if req.ID != 1 {
		t.Errorf("id = %d, want 1", req.ID)
	}
	if req.Method != "initialize" {
		t.Errorf("method = %q, want initialize", req.Method)
	}
	// Should end with newline.
	if data[len(data)-1] != '\n' {
		t.Error("missing trailing newline")
	}
}

func TestEncodeNotification(t *testing.T) {
	data, err := encodeNotification("session/cancel", sessionCancelParams{SessionID: "abc"})
	if err != nil {
		t.Fatalf("encodeNotification: %v", err)
	}
	var req struct {
		JSONRPC string `json:"jsonrpc"`
		ID      *int   `json:"id,omitempty"`
		Method  string `json:"method"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.ID != nil {
		t.Error("notification should not have id")
	}
	if req.Method != "session/cancel" {
		t.Errorf("method = %q", req.Method)
	}
}

func TestDecodeResponse(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"test-123"}}`)
	resp, req, notif, err := decodeMessage(line)
	if err != nil {
		t.Fatalf("decodeMessage: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if req != nil || notif != nil {
		t.Fatal("expected only response")
	}
	var idNum int
	if err := json.Unmarshal(resp.ID, &idNum); err != nil {
		t.Fatalf("unmarshal id: %v", err)
	}
	if idNum != 1 {
		t.Errorf("id = %d, want 1", idNum)
	}
}

func TestDecodeRequest(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":5,"method":"session/request_permission","params":{"sessionId":"s1","toolCall":{"toolCallId":"tc1"},"options":[]}}`)
	resp, req, notif, err := decodeMessage(line)
	if err != nil {
		t.Fatalf("decodeMessage: %v", err)
	}
	if req == nil {
		t.Fatal("expected request, got nil")
	}
	if resp != nil || notif != nil {
		t.Fatal("expected only request")
	}
	if req.Method != "session/request_permission" {
		t.Errorf("method = %q", req.Method)
	}
}

func TestDecodeNotification(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}`)
	resp, req, notif, err := decodeMessage(line)
	if err != nil {
		t.Fatalf("decodeMessage: %v", err)
	}
	if notif == nil {
		t.Fatal("expected notification, got nil")
	}
	if resp != nil || req != nil {
		t.Fatal("expected only notification")
	}
	if notif.Method != "session/update" {
		t.Errorf("method = %q", notif.Method)
	}
}

func TestConvertSessionUpdate_TextChunk(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello world"}}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventText {
		t.Errorf("type = %v, want EventText", events[0].Type)
	}
	if events[0].Content != "hello world" {
		t.Errorf("content = %q", events[0].Content)
	}
}

func TestConvertSessionUpdate_ThinkingChunk(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"thinking..."}}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventThinking {
		t.Errorf("type = %v, want EventThinking", events[0].Type)
	}
}

func TestConvertSessionUpdate_ToolCallCompleted(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"Read file","kind":"read","status":"completed"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventToolResult {
		t.Errorf("type = %v, want EventToolResult", events[0].Type)
	}
	if events[0].ToolSuccess == nil || !*events[0].ToolSuccess {
		t.Error("expected ToolSuccess=true")
	}
}

func TestConvertSessionUpdate_ToolCallFailed(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"Write file","kind":"edit","status":"failed"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventToolResult {
		t.Errorf("type = %v, want EventToolResult", events[0].Type)
	}
	if events[0].ToolSuccess == nil || *events[0].ToolSuccess {
		t.Error("expected ToolSuccess=false")
	}
}

func TestConvertSessionUpdate_ToolCallPending(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"Run command","kind":"execute","status":"pending"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventToolUse {
		t.Errorf("type = %v, want EventToolUse", events[0].Type)
	}
}

func TestConvertSessionUpdate_Plan(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"plan","entries":[{"content":"Step 1","priority":"high","status":"pending"},{"content":"Step 2","priority":"medium","status":"completed"}]}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventPlan {
		t.Errorf("type = %v, want EventPlan", events[0].Type)
	}
	if len(events[0].Plan) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(events[0].Plan))
	}
	if events[0].Plan[0].Content != "Step 1" {
		t.Errorf("todo[0] content = %q", events[0].Plan[0].Content)
	}
}

func TestConvertSessionUpdate_UsageUpdate(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"usage_update","used":5000,"size":200000}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventContextUsageUpdated {
		t.Errorf("type = %v, want EventContextUsageUpdated", events[0].Type)
	}
	if events[0].ContextUsage == nil {
		t.Fatal("expected ContextUsage non-nil")
	}
	if events[0].ContextUsage.UsedTokens != 5000 {
		t.Errorf("UsedTokens = %d, want 5000", events[0].ContextUsage.UsedTokens)
	}
	if events[0].ContextUsage.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", events[0].ContextUsage.ContextWindow)
	}
}

func TestConvertSessionUpdate_UserMessageChunkIgnored(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"echo"}}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestConvertSessionUpdate_UnknownType(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"future_feature","data":"stuff"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventError {
		t.Errorf("type = %v, want EventError", events[0].Type)
	}
}

func TestConvertSessionUpdate_TruncatedJSON(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chu`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventError {
		t.Errorf("type = %v, want EventError", events[0].Type)
	}
}

func TestSelectPermissionOption_Allow(t *testing.T) {
	options := []permissionOption{
		{OptionID: "a1", Name: "Allow once", Kind: "allow_once"},
		{OptionID: "a2", Name: "Allow always", Kind: "allow_always"},
		{OptionID: "r1", Name: "Reject once", Kind: "reject_once"},
	}
	id, ok := selectPermissionOption(options, "allow")
	if !ok {
		t.Fatal("expected to find allow option")
	}
	if id != "a1" {
		t.Errorf("optionId = %q, want a1", id)
	}
}

func TestSelectPermissionOption_Deny(t *testing.T) {
	options := []permissionOption{
		{OptionID: "a1", Name: "Allow once", Kind: "allow_once"},
		{OptionID: "r1", Name: "Reject once", Kind: "reject_once"},
		{OptionID: "r2", Name: "Reject always", Kind: "reject_always"},
	}
	id, ok := selectPermissionOption(options, "deny")
	if !ok {
		t.Fatal("expected to find reject option")
	}
	if id != "r1" {
		t.Errorf("optionId = %q, want r1", id)
	}
}

func TestSelectPermissionOption_NoMatch(t *testing.T) {
	options := []permissionOption{
		{OptionID: "a1", Name: "Allow once", Kind: "allow_once"},
	}
	_, ok := selectPermissionOption(options, "deny")
	if ok {
		t.Fatal("expected no match for deny")
	}
}

func TestAcpFlag_UnmarshalBoolAndObject(t *testing.T) {
	var f acpFlag
	if err := json.Unmarshal([]byte("true"), &f); err != nil || !f.Enabled {
		t.Fatalf("true: err=%v enabled=%v", err, f.Enabled)
	}
	f = acpFlag{}
	if err := json.Unmarshal([]byte("false"), &f); err != nil || f.Enabled {
		t.Fatalf("false: err=%v enabled=%v", err, f.Enabled)
	}
	f = acpFlag{}
	if err := json.Unmarshal([]byte("{}"), &f); err != nil || !f.Enabled {
		t.Fatalf("{}: err=%v enabled=%v", err, f.Enabled)
	}
}

func TestInitializeResult_LoadSessionBool(t *testing.T) {
	// Minimal shape matching real Grok CLI 0.2.93 initialize result.
	raw := []byte(`{
		"protocolVersion": 1,
		"agentCapabilities": {
			"loadSession": true,
			"promptCapabilities": {"image": false}
		},
		"authMethods": [{"id": "cached_token", "name": "cached_token"}]
	}`)
	var res initializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.AgentCapabilities == nil || !res.AgentCapabilities.LoadSession.Enabled {
		t.Fatalf("loadSession not enabled: %+v", res.AgentCapabilities)
	}
}
