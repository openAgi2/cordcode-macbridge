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

func TestConvertSessionUpdate_AutoCompactStarted(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"auto_compact_started","tokens_used":400576,"context_window":500000,"percentage":80}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 || events[0].Type != core.EventContextUsageUpdated {
		t.Fatalf("events = %+v", events)
	}
	if events[0].ContextUsage.UsedTokens != 400576 || events[0].ContextUsage.ContextWindow != 500000 {
		t.Fatalf("usage = %+v", events[0].ContextUsage)
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

func TestConvertSessionUpdate_UserMessageChunk(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"讲个法国笑话"}}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != core.EventUserMessage || ev.Content != "讲个法国笑话" {
		t.Fatalf("event = %+v, want EventUserMessage with prompt text", ev)
	}
	// user_message_chunk 不带 promptId; 身份必须由 relay 用同 turn 首个内容事件的
	// promptId 补齐, 这里不得合成或猜测身份。
	if ev.ItemID != "" || ev.TurnID != "" {
		t.Fatalf("identity must be deferred, got itemId=%q turnId=%q", ev.ItemID, ev.TurnID)
	}
}

func TestConvertSessionUpdate_UnknownType(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"future_feature","data":"stuff"}}`)
	events := convertSessionUpdate(params, "s1")
	// Unknown update types must NOT produce events — emitting EventError would
	// abort turns whenever Grok sends an extension type we haven't mapped.
	if len(events) != 0 {
		t.Fatalf("expected 0 events for unknown type, got %d: %+v", len(events), events)
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

// TestConvertSessionUpdate_TurnCompleted_* 覆盖上游 durable 终态信号映射。
// 真实 updates.jsonl 样本形状: {"sessionUpdate":"turn_completed","prompt_id":"...","stop_reason":"..."}
// 上游对 prompt_id 的 JSON key 不一致 (prompt_id 440次 / promptId 289次), 两种都要兼容。

func TestConvertSessionUpdate_TurnCompleted_EndTurn(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"turn_completed","prompt_id":"ad68fb4b","stop_reason":"end_turn"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	if events[0].Type != core.EventResult {
		t.Errorf("type = %v, want EventResult", events[0].Type)
	}
	if !events[0].Done {
		t.Error("expected Done=true")
	}
	if events[0].TurnID != "ad68fb4b" {
		t.Errorf("TurnID = %q, want ad68fb4b", events[0].TurnID)
	}
}

func TestConvertSessionUpdate_TurnCompleted_PromptIdCamelCase(t *testing.T) {
	// 上游新版本用 camelCase promptId, 旧版本用 snake_case prompt_id — 两者都要能取到。
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"turn_completed","promptId":"camel-id","stop_reason":"end_turn"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].TurnID != "camel-id" {
		t.Errorf("TurnID = %q, want camel-id", events[0].TurnID)
	}
}

func TestConvertSessionUpdate_TurnCompleted_Cancelled(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"turn_completed","prompt_id":"p-cancel","stop_reason":"cancelled"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventResult || !events[0].Done {
		t.Errorf("expected EventResult Done, got %+v", events[0])
	}
	if events[0].TurnID != "p-cancel" {
		t.Errorf("TurnID = %q, want p-cancel", events[0].TurnID)
	}
}

func TestConvertSessionUpdate_TurnCompleted_Error(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"turn_completed","prompt_id":"p-err","stop_reason":"error"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventError {
		t.Errorf("type = %v, want EventError for stop_reason=error", events[0].Type)
	}
	if !events[0].Done {
		t.Error("expected Done=true (terminal)")
	}
	if events[0].TurnID != "p-err" {
		t.Errorf("TurnID = %q, want p-err", events[0].TurnID)
	}
}

func TestConvertSessionUpdate_TurnCompleted_RateLimit(t *testing.T) {
	// rate_limit 应映射成正常完成 (EventResult), 不是 error — 限流是可恢复的, 不应显示为失败。
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"turn_completed","prompt_id":"p-rl","stop_reason":"rate_limit"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventResult {
		t.Errorf("type = %v, want EventResult (rate_limit is recoverable)", events[0].Type)
	}
	if !events[0].Done {
		t.Error("expected Done=true")
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

// TestConvertSessionUpdate_ArrayContentToolCallUpdate 不再因数组 content 报 EventError。
// 真实 updates.jsonl 里 tool_call_update 的 content 约一半是数组形状
// ([{type:"content",content:{type:"text",text:"..."}}])。旧实现用 *contentBlock 解析,
// 数组会让整个 outer unmarshal 失败 → EventError → relay loop 误判终态 → idle/running 振荡。
// 修复后 outer 解析成功, completed 状态正常映射成 EventToolResult。
func TestConvertSessionUpdate_ArrayContentToolCallUpdate(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"tc-arr","title":"Execute","status":"completed","content":[{"type":"content","content":{"type":"text","text":"done"}}]},"_meta":{"promptId":"p-arr"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event (EventToolResult), got %d: %+v", len(events), events)
	}
	if events[0].Type != core.EventToolResult {
		t.Errorf("type = %v, want EventToolResult (array content must not cause EventError)", events[0].Type)
	}
	if events[0].Type == core.EventError {
		t.Fatalf("array content regressed to EventError: %+v", events[0])
	}
	if events[0].RequestID != "tc-arr" {
		t.Errorf("RequestID = %q, want tc-arr", events[0].RequestID)
	}
	if events[0].TurnID != "p-arr" {
		t.Errorf("TurnID = %q, want p-arr", events[0].TurnID)
	}
}

// 数组 content + 非终态 status (in_progress) → 0 events, 且绝不是 EventError。
func TestConvertSessionUpdate_ArrayContentNonTerminalNoError(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"tc-ip","status":"in_progress","content":[{"type":"content","content":{"type":"text","text":"running"}}]},"_meta":{"promptId":"p-ip"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 0 {
		t.Fatalf("expected 0 events for in_progress tool_call_update, got %d: %+v", len(events), events)
	}
}

// TestConvertSessionUpdate_ChunkPromptIDIdentity 是 Bug2 的核心回归:
// agent_message_chunk 必须把 _meta.promptId 透传成 ItemID/TurnID, 否则 SSV2 projection
// reducer 会把 identityless text_delta 直接 skip (projection_reducer.go:537),
// iOS syncV2 连接的 raw timeline 又被 seal → 流式正文两端都到不了 iOS (真机 "无反应")。
func TestConvertSessionUpdate_ChunkPromptIDIdentity(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}},"_meta":{"promptId":"73494e8f-ce09-49ce-9fcc-a2a92ca4d172"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventText {
		t.Errorf("type = %v, want EventText", events[0].Type)
	}
	if events[0].Content != "hello" {
		t.Errorf("content = %q, want hello", events[0].Content)
	}
	if events[0].ItemID != "73494e8f-ce09-49ce-9fcc-a2a92ca4d172" {
		t.Errorf("ItemID = %q, want promptId (reducer will skip empty itemId)", events[0].ItemID)
	}
	if events[0].TurnID != "73494e8f-ce09-49ce-9fcc-a2a92ca4d172" {
		t.Errorf("TurnID = %q, want promptId (relay loop synthesizes turn_started from it)", events[0].TurnID)
	}
}

// agent_thought_chunk 同样要透传 promptId (reducer reasoning_delta 也要求 itemId)。
func TestConvertSessionUpdate_ThinkingPromptIDIdentity(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"thinking..."}},"_meta":{"promptId":"p-thought"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventThinking {
		t.Errorf("type = %v, want EventThinking", events[0].Type)
	}
	if events[0].ItemID != "p-thought" {
		t.Errorf("ItemID = %q, want p-thought", events[0].ItemID)
	}
}

// prompt_id snake_case 兜底 (上游 _meta 偶用 snake_case)。
func TestConvertSessionUpdate_ChunkPromptIDSnakeCase(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}},"_meta":{"prompt_id":"p-snake"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 || events[0].ItemID != "p-snake" {
		t.Fatalf("expected ItemID=p-snake, got %+v", events)
	}
}

// tool_call 用 toolCallId 作 RequestID (per-tool 稳定 id, reducer tool_started 的 itemId),
// promptId 作 TurnID (挂到正确 turn)。无 toolCallId 时不冒充 (留空 → reducer skip, 符合 SSV2)。
func TestConvertSessionUpdate_ToolCallIdentity(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"call-abc-0","title":"read_file"},"_meta":{"promptId":"p-turn"}}`)
	events := convertSessionUpdate(params, "s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventToolUse {
		t.Errorf("type = %v, want EventToolUse", events[0].Type)
	}
	if events[0].RequestID != "call-abc-0" {
		t.Errorf("RequestID = %q, want call-abc-0", events[0].RequestID)
	}
	if events[0].TurnID != "p-turn" {
		t.Errorf("TurnID = %q, want p-turn", events[0].TurnID)
	}
}

// contentText 单元测试: 单对象 vs 嵌套数组两种真实形状都能抽出正文。
func TestContentText_SingleObject(t *testing.T) {
	p := sessionUpdatePayload{Content: json.RawMessage(`{"type":"text","text":"hi there"}`)}
	if got := p.contentText(); got != "hi there" {
		t.Errorf("contentText() = %q, want %q", got, "hi there")
	}
}

func TestContentText_NestedArrayShape(t *testing.T) {
	// 真实 tool_call_update 数组形状: [{type:"content", content:{type:"text", text:"..."}}]
	p := sessionUpdatePayload{Content: json.RawMessage(`[{"type":"content","content":{"type":"text","text":"List src"}}]`)}
	if got := p.contentText(); got != "List src" {
		t.Errorf("contentText() = %q, want %q", got, "List src")
	}
}

func TestContentText_NullAndEmpty(t *testing.T) {
	p := sessionUpdatePayload{Content: json.RawMessage(`null`)}
	if got := p.contentText(); got != "" {
		t.Errorf("null contentText() = %q, want empty", got)
	}
	if p.hasContent() {
		t.Error("null content should report hasContent=false")
	}
	empty := sessionUpdatePayload{}
	if empty.hasContent() {
		t.Error("missing content should report hasContent=false")
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
