package gobridge

// plan_review_wire_test.go —— 计划审批层 Phase 1（方案 §4/§5 Phase 1）wire 扩展测试：
// 1. permission_request.plan 载荷序列化（contentFormat 默认 markdown、可选字段省略）；
// 2. 旧形状兼容回归——无 PlanReview 的事件 wire 字节级不变（含 grok §25 最小档生产形状）；
// 3. resolve_permission.planAction/feedback 归一化（approve→allow；requestChanges/quit→deny；
//    未知 planAction fail-closed；feedback 进 Message）；
// 4. SSV2 投影面——plan 载荷落到 projected part（v2 客户端 raw permission_request 被
//    seal，PermissionPlan 是 iOS 计划卡唯一数据面），thin 重复帧不擦除；
// 5. relay 帧预算锚定——8KB 级 plan 载荷过 event→wire→reducer 全链无截断，入站调度
//    队列预算有余量（方案 §2.2 结论的行为化锚定）。

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestMapAgentEventPermissionRequestPlanPayload(t *testing.T) {
	name, data, done := mapAgentEvent(core.Event{
		Type:              core.EventPermissionRequest,
		RequestID:         "plan_1",
		ToolName:          "Plan approval",
		SessionID:         "s1",
		PermissionKind:    "plan_review",
		PermissionActions: []string{"approve", "requestChanges", "quit"},
		PlanReview: &core.PlanPayload{
			Content:      "# Plan\n\n1. Step one\n2. Step two",
			Title:        "Plan",
			PlanFilePath: "/tmp/plan-abc123.md",
		},
	})
	if name != "permission_request" || done {
		t.Fatalf("name=%q done=%v", name, done)
	}
	payload := data.(map[string]interface{})
	plan, ok := payload["plan"].(map[string]interface{})
	if !ok {
		t.Fatalf("plan payload missing: %#v", payload)
	}
	if got := plan["content"]; got != "# Plan\n\n1. Step one\n2. Step two" {
		t.Fatalf("plan.content = %#v", got)
	}
	// contentFormat 默认 markdown（消费方不猜格式）。
	if got := plan["contentFormat"]; got != "markdown" {
		t.Fatalf("plan.contentFormat = %#v, want markdown (default)", got)
	}
	if got := plan["title"]; got != "Plan" {
		t.Fatalf("plan.title = %#v", got)
	}
	if got := plan["planFilePath"]; got != "/tmp/plan-abc123.md" {
		t.Fatalf("plan.planFilePath = %#v", got)
	}

	// 显式 contentFormat 透传，不覆盖来源声明。
	_, data2, _ := mapAgentEvent(core.Event{
		Type:           core.EventPermissionRequest,
		RequestID:      "plan_2",
		PermissionKind: "plan_review",
		PlanReview:     &core.PlanPayload{Content: "x", ContentFormat: "markdown"},
	})
	plan2 := data2.(map[string]interface{})["plan"].(map[string]interface{})
	if got := plan2["contentFormat"]; got != "markdown" {
		t.Fatalf("explicit contentFormat = %#v", got)
	}
	// 可选字段为空时省略（title/planFilePath 不出现）。
	if _, present := plan2["title"]; present {
		t.Fatal("empty title must be omitted")
	}
	if _, present := plan2["planFilePath"]; present {
		t.Fatal("empty planFilePath must be omitted")
	}
}

// TestMapAgentEventPermissionRequestLegacyShapeByteCompat：无 PlanReview 的旧事件
// wire 字节级不变（含 grok §25 最小档生产形状——两键 permissionActions、无
// permissionKind/plan）。硬编码 JSON 串作为回归锚点，防 map 构造漂移。
func TestMapAgentEventPermissionRequestLegacyShapeByteCompat(t *testing.T) {
	_, data, _ := mapAgentEvent(core.Event{
		Type:              core.EventPermissionRequest,
		RequestID:         "grok-wire-1",
		ToolName:          "Approve plan: Fix login flow",
		PermissionActions: []string{"approve", "reject"},
	})
	payload := data.(map[string]interface{})
	if _, present := payload["plan"]; present {
		t.Fatal("plan must be absent for non-plan permission requests")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"permissionActions":["approve","reject"],"requestId":"grok-wire-1","toolInput":"","toolInputRaw":null,"toolName":"Approve plan: Fix login flow"}`
	if string(raw) != want {
		t.Fatalf("legacy wire shape drifted:\n got: %s\nwant: %s", raw, want)
	}

	// 极简（thin）形状同样不含 plan 键。
	_, dataThin, _ := mapAgentEvent(core.Event{
		Type:      core.EventPermissionRequest,
		RequestID: "req_1",
		ToolName:  "bash",
	})
	if _, present := dataThin.(map[string]interface{})["plan"]; present {
		t.Fatal("plan must be absent for thin permission requests")
	}
}

// planReviewCaptureSession 记录 RespondPermission 收到的 PermissionResult。
type planReviewCaptureSession struct {
	*fakeAgentSession
	mu      sync.Mutex
	results []core.PermissionResult
}

func (s *planReviewCaptureSession) RespondPermission(_ string, r core.PermissionResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, r)
	return nil
}

func (s *planReviewCaptureSession) captured() []core.PermissionResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.PermissionResult(nil), s.results...)
}

func resolvePlanViaHandler(t *testing.T, h *Handlers, params map[string]any) *userInputCaptureConn {
	t.Helper()
	raw, _ := json.Marshal(params)
	conn := &userInputCaptureConn{}
	h.handleResolvePermission(conn, WireMessage{Type: "request", RequestID: "req-plan", BackendID: "grokbuild", Method: "resolve_permission", Params: raw})
	return conn
}

// TestResolvePermissionPlanActionTranslation：planAction 归一化 + 行为兼容映射
// （方案 §4.2：approve→allow，requestChanges/quit→deny；未知 fail-closed）。
func TestResolvePermissionPlanActionTranslation(t *testing.T) {
	h := newTestHandlers(t)
	sess := &planReviewCaptureSession{fakeAgentSession: &fakeAgentSession{id: "s-plan", events: make(chan core.Event, 8)}}
	h.putSessionWithMeta("s-plan", "grokbuild", "", sess)

	if conn := resolvePlanViaHandler(t, h, map[string]any{
		"sessionId": "s-plan", "requestId": "plan_1",
		"behavior": "deny", "planAction": "approve",
	}); conn.wireErr != nil {
		t.Fatalf("approve resolve err = %+v", conn.wireErr)
	}
	got := sess.captured()
	if len(got) != 1 || got[0].Behavior != "allow" || got[0].PlanAction != "approve" {
		t.Fatalf("approve translation = %+v", got)
	}

	if conn := resolvePlanViaHandler(t, h, map[string]any{
		"sessionId": "s-plan", "requestId": "plan_1",
		"behavior": "deny", "planAction": "requestChanges", "feedback": "第二步改成并行",
	}); conn.wireErr != nil {
		t.Fatalf("requestChanges resolve err = %+v", conn.wireErr)
	}
	got = sess.captured()
	if len(got) != 2 || got[1].Behavior != "deny" || got[1].PlanAction != "requestChanges" || got[1].Message != "第二步改成并行" {
		t.Fatalf("requestChanges translation = %+v", got[1])
	}

	if conn := resolvePlanViaHandler(t, h, map[string]any{
		"sessionId": "s-plan", "requestId": "plan_1",
		"behavior": "deny", "planAction": "quit",
	}); conn.wireErr != nil {
		t.Fatalf("quit resolve err = %+v", conn.wireErr)
	}
	got = sess.captured()
	if len(got) != 3 || got[2].Behavior != "deny" || got[2].PlanAction != "quit" || got[2].Message != "" {
		t.Fatalf("quit translation = %+v", got[2])
	}

	// 未知 planAction：fail-closed，不得静默按 allow/deny 兜底。
	conn := resolvePlanViaHandler(t, h, map[string]any{
		"sessionId": "s-plan", "requestId": "plan_1",
		"behavior": "allow", "planAction": "explode",
	})
	if conn.wireErr == nil || conn.wireErr.Code != "invalid_params" {
		t.Fatalf("unknown planAction must fail closed, got %+v", conn.wireErr)
	}
	if n := len(sess.captured()); n != 3 {
		t.Fatalf("unknown planAction must not reach backend, calls = %d", n)
	}

	// 旧客户端：仅 behavior，无 planAction——PermissionResult 与扩展前完全一致。
	if conn := resolvePlanViaHandler(t, h, map[string]any{
		"sessionId": "s-plan", "requestId": "plan_1", "behavior": "allow",
	}); conn.wireErr != nil {
		t.Fatalf("legacy resolve err = %+v", conn.wireErr)
	}
	got = sess.captured()
	if len(got) != 4 || got[3].Behavior != "allow" || got[3].PlanAction != "" || got[3].Message != "" {
		t.Fatalf("legacy translation = %+v", got[3])
	}
}

// TestReducerPermissionRequestCarriesPlanPayload：SSV2 投影面——plan 载荷落到
// projected part（v2 客户端被 seal 在 raw permission_request 之外），thin 重复帧
// 不擦除（mergeToolPart 语义与 official payload 一致）。
func TestReducerPermissionRequestCarriesPlanPayload(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "grokbuild", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	planPayload := map[string]interface{}{
		"content":       "# Plan\n\n1. Step one",
		"contentFormat": "markdown",
		"title":         "Plan",
	}
	r.Apply(ev(2, "grokbuild", "s1", "permission_request", map[string]interface{}{
		"requestId":         "plan-wire-1",
		"toolName":          "Plan approval",
		"permissionKind":    "plan_review",
		"permissionActions": []interface{}{"approve", "requestChanges", "quit"},
		"plan":              planPayload,
	}))
	proj, ok := r.Snapshot("grokbuild", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	var part *ProjectionPart
	for i, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "plan-wire-1" {
			part = &proj.Turns[0].Assistant.Parts[i]
			break
		}
	}
	if part == nil {
		t.Fatalf("missing permission part: %+v", proj.Turns[0].Assistant.Parts)
	}
	if part.PermissionKind != "plan_review" || !part.RequiresPermissionConfirmation {
		t.Fatalf("permission part = %+v", part)
	}
	plan, ok := part.PermissionPlan.(map[string]interface{})
	if !ok || plan["content"] != "# Plan\n\n1. Step one" || plan["contentFormat"] != "markdown" {
		t.Fatalf("PermissionPlan = %#v", part.PermissionPlan)
	}

	// 同 id thin 重复帧（stale/重放）不得擦除已投影 plan 载荷。
	r.Apply(ev(3, "grokbuild", "s1", "permission_request", map[string]interface{}{
		"requestId": "plan-wire-1",
		"toolName":  "Plan approval",
	}))
	proj, _ = r.Snapshot("grokbuild", "s1")
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "plan-wire-1" {
			if p.PermissionPlan == nil {
				t.Fatal("thin duplicate must not erase projected plan payload")
			}
			return
		}
	}
	t.Fatal("permission part lost after thin duplicate")
}

// TestPermissionRequestPlanPayload8KBSurvivesWireAndProjection：8KB 级 plan 载荷
// 过 mapAgentEvent → json marshal → reducer → 投影全链无截断（方案 §2.2 relay 帧预算
// 结论的行为化锚定：所有层预算 ≥8MiB，10KB 级载荷余量 ≥3 个数量级）。
func TestPermissionRequestPlanPayload8KBSurvivesWireAndProjection(t *testing.T) {
	line := "1. 修复登录流程：校验回调 state 参数并写入 session cookie。\n"
	content := "# Plan\n\n" + strings.Repeat(line, 148) // ≈8.1KB
	if len(content) < 8*1024 {
		t.Fatalf("fixture too small: %d bytes", len(content))
	}

	evCore := core.Event{
		Type:              core.EventPermissionRequest,
		RequestID:         "plan-8k",
		ToolName:          "Plan approval",
		PermissionKind:    "plan_review",
		PermissionActions: []string{"approve", "requestChanges", "quit"},
		PlanReview:        &core.PlanPayload{Content: content},
	}
	_, data, _ := mapAgentEvent(evCore)
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 8*1024 {
		t.Fatalf("wire frame unexpectedly small: %d bytes", len(raw))
	}
	var roundTrip map[string]interface{}
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}

	r := newTestReducer()
	r.Apply(ev(1, "grokbuild", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "grokbuild", "s1", "permission_request", roundTrip))
	proj, ok := r.Snapshot("grokbuild", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "plan-8k" {
			plan, ok := p.PermissionPlan.(map[string]interface{})
			if !ok {
				t.Fatalf("PermissionPlan = %#v", p.PermissionPlan)
			}
			got, _ := plan["content"].(string)
			if got != content {
				t.Fatalf("plan content truncated: got %d bytes, want %d", len(got), len(content))
			}
			return
		}
	}
	t.Fatal("permission part missing")
}

// TestRelayInboundSchedulerAcceptsPlanSizedFrame：8KB 级 resolve/事件帧入站调度
// 零压力（队列预算 256 帧 / 8MiB，方案 §2.2 表）。
func TestRelayInboundSchedulerAcceptsPlanSizedFrame(t *testing.T) {
	dispatched := make(chan string, 2)
	s := newRelayInboundScheduler(func(msg WireMessage) { dispatched <- msg.RequestID }, nil)
	defer s.close()
	frame := inboundRequest("plan-resolve-1", "resolve_permission", "s1")
	if err := s.enqueue(frame); err != nil {
		t.Fatal(err)
	}
	// 8KB 级帧（携带大 feedback / 邻近载荷的保守放大）必须正常入队。
	big := make([]byte, 8*1024)
	for i := range big {
		big[i] = 'a'
	}
	bigFrame, _ := json.Marshal(map[string]any{
		"type": "request", "requestId": "plan-resolve-2", "method": "resolve_permission",
		"params": map[string]any{"sessionId": "s1", "requestId": "plan-8k", "behavior": "deny", "planAction": "requestChanges", "feedback": string(big)},
	})
	if err := s.enqueue(bigFrame); err != nil {
		t.Fatalf("8KB plan-sized frame rejected by inbound scheduler: %v", err)
	}
	for i := 0; i < 2; i++ {
		<-dispatched
	}
}
