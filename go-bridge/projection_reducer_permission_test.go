package gobridge

import "testing"

func TestReducerQuestionAskedProjectsUserInputDock(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "dsh-web", "s1", "question_asked", map[string]interface{}{
		"questionId":   "q-hulwa",
		"questionText": "葫芦娃故事.txt 已经创建过了。您现在想让我做什么？",
		"options": []interface{}{
			map[string]interface{}{"id": "再新建一个不同版本", "label": "再新建一个不同版本"},
			map[string]interface{}{"id": "覆盖现有文件", "label": "覆盖现有文件"},
			map[string]interface{}{"id": "不需要了", "label": "不需要了"},
		},
	}))
	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	if proj.Execution.Phase != "requires_action" {
		t.Fatalf("phase = %q, want requires_action", proj.Execution.Phase)
	}
	found := false
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "user_input" && p.UserInputInteractionID == "q-hulwa" {
			found = true
			if p.UserInputStatus != "pending" || !p.UserInputCanRespond {
				t.Fatalf("user_input part = %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("missing user_input part: %+v", proj.Turns[0].Assistant.Parts)
	}

	r.Apply(ev(3, "dsh-web", "s1", "question_resolved", map[string]interface{}{
		"questionId": "q-hulwa",
		"result":     "answered",
	}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "user_input" && p.UserInputInteractionID == "q-hulwa" {
			if p.UserInputStatus != "answered" {
				t.Fatalf("resolved status = %q", p.UserInputStatus)
			}
			return
		}
	}
	t.Fatal("resolved user_input missing")
}

func TestReducerPermissionRequestProjectsPendingToolAndRequiresAction(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "dsh-web", "s1", "permission_request", map[string]interface{}{
		"requestId": "appr-write",
		"toolName":  "write",
	}))

	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	if proj.Execution.Phase != "requires_action" || proj.Execution.ActiveTurnID != "T1" {
		t.Fatalf("execution = %+v, want requires_action/T1", proj.Execution)
	}
	if len(proj.Turns) == 0 || proj.Turns[0].Assistant == nil {
		t.Fatalf("missing assistant: %+v", proj.Turns)
	}
	found := false
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "appr-write" {
			found = true
			if p.ToolName != "write" || p.ToolStatus != "pending" || !p.RequiresPermissionConfirmation {
				t.Fatalf("permission tool part = %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("missing permission tool part: %+v", proj.Turns[0].Assistant.Parts)
	}
}

func TestReducerPermissionRequestKeepsReasonAsTitle(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "dsh-web", "s1", "permission_request", map[string]interface{}{
		"requestId": "appr-bash",
		"toolName":  "bash",
		"reason":    "escalate sandbox to danger-full-access: 超出工作区",
	}))
	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "appr-bash" {
			if p.Title != "escalate sandbox to danger-full-access: 超出工作区" || !p.RequiresPermissionConfirmation {
				t.Fatalf("reason title missing: %+v", p)
			}
			return
		}
	}
	t.Fatalf("missing bash permission part: %+v", proj.Turns[0].Assistant.Parts)
}

// 官方载荷（opencode-web v1.18，live-pinned permission.asked）：permissionKind/
// patterns 必须落到投影 part（SSV2 权限卡的 SoT），且同 id 薄事件后到不得抹掉
//（双 backend 订阅同一 serve 的竞态：老 opencode 与 opencode-web 各发一条）。
func TestReducerPermissionRequestCarriesOfficialPayloadAndThinMergeKeepsIt(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "opencode-web", "ses_1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "opencode-web", "ses_1", "permission_request", map[string]interface{}{
		"requestId":      "per_9",
		"toolName":       "external_directory",
		"permissionKind": "external_directory",
		"patterns":       []interface{}{"/Users/jacklee/Projects/Chat/*"},
		"toolInput":      "/Users/jacklee/Projects/Chat/红楼梦故事.txt",
	}))
	// 同 id 薄载荷（老 backend 竞态）后到。
	r.Apply(ev(3, "opencode", "ses_1", "permission_request", map[string]interface{}{
		"requestId": "per_9",
		"toolName":  "",
	}))

	proj, ok := r.Snapshot("opencode-web", "ses_1")
	if !ok {
		t.Fatal("no projection")
	}
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "per_9" {
			if !p.RequiresPermissionConfirmation || p.ToolStatus != "pending" {
				t.Fatalf("pending permission part = %+v", p)
			}
			if p.PermissionKind != "external_directory" {
				t.Fatalf("permissionKind = %q, want external_directory (thin merge must keep official fields)", p.PermissionKind)
			}
			if len(p.PermissionPatterns) != 1 || p.PermissionPatterns[0] != "/Users/jacklee/Projects/Chat/*" {
				t.Fatalf("permissionPatterns = %v", p.PermissionPatterns)
			}
			return
		}
	}
	t.Fatalf("missing permission part: %+v", proj.Turns[0].Assistant.Parts)
}

func TestReducerPermissionResolvedClearsPendingAndLeavesRunning(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "dsh-web", "s1", "permission_request", map[string]interface{}{
		"requestId": "appr-write",
		"toolName":  "write",
	}))
	r.Apply(ev(3, "dsh-web", "s1", "permission_resolved", map[string]interface{}{
		"requestId": "appr-write",
		"behavior":  "allow",
	}))

	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	if proj.Execution.Phase != "running" || proj.Execution.ActiveTurnID != "T1" {
		t.Fatalf("execution = %+v, want running/T1", proj.Execution)
	}
	found := false
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "appr-write" {
			found = true
			if p.RequiresPermissionConfirmation || p.ToolStatus != "running" {
				t.Fatalf("resolved permission tool = %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("missing resolved tool part: %+v", proj.Turns[0].Assistant.Parts)
	}

	// Idempotent second resolve (host approval/resolved after iOS Allow).
	r.Apply(ev(4, "dsh-web", "s1", "permission_resolved", map[string]interface{}{
		"requestId": "appr-write",
		"behavior":  "allow",
	}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	if proj.Execution.Phase != "running" {
		t.Fatalf("second resolve phase = %q", proj.Execution.Phase)
	}
}

// persist-only turn_started must not publish a skeleton, but the following
// permission_request has to ship the turn shell — otherwise iOS applyingPartOps
// skips upsert_tool (owner 2026-08-16: Mac 覆盖后出权限框，iPhone 没有).
func TestReducerPermissionRequestAfterPersistOnlyTurnStartedFlushesTurnShell(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T-new"}))
	if patch, ok := r.FlushPatch("dsh-web", "s1"); ok {
		t.Fatalf("turn_started must stay persist-only, got %+v", patch)
	}

	r.Apply(ev(2, "dsh-web", "s1", "permission_request", map[string]interface{}{
		"requestId": "appr-write",
		"toolName":  "write",
		"reason":    "escalate sandbox to danger-full-access: 超出工作区",
	}))
	patch, ok := r.FlushPatch("dsh-web", "s1")
	if !ok {
		t.Fatal("expected permission patch")
	}
	foundTurn := false
	for _, turn := range patch.UpsertTurns {
		if turn.TurnID == "T-new" {
			foundTurn = true
			if turn.Assistant == nil {
				t.Fatal("upserted turn missing assistant")
			}
			hasTool := false
			for _, part := range turn.Assistant.Parts {
				if part.Type == "tool" && part.ItemID == "appr-write" && part.RequiresPermissionConfirmation {
					hasTool = true
				}
			}
			if !hasTool {
				t.Fatalf("upserted turn missing permission tool: %+v", turn.Assistant.Parts)
			}
		}
	}
	if !foundTurn {
		t.Fatalf("UpsertTurns missing T-new: %+v", patch.UpsertTurns)
	}
	foundTool := false
	for _, op := range patch.PartOps {
		if op.Op == "upsert_tool" && op.TurnID == "T-new" && op.Part != nil && op.Part.ItemID == "appr-write" {
			foundTool = true
			if !op.Part.RequiresPermissionConfirmation || op.Part.ToolStatus != "pending" {
				t.Fatalf("upsert_tool part = %+v", op.Part)
			}
		}
	}
	if !foundTool {
		t.Fatalf("missing upsert_tool on T-new: %+v", patch.PartOps)
	}
	if patch.Execution == nil || patch.Execution.Phase != "requires_action" {
		t.Fatalf("execution = %+v, want requires_action", patch.Execution)
	}
}

func TestReducerUserInputRequestedWithoutTurnIDUsesActiveTurn(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T-new"}))
	r.Apply(ev(2, "dsh-web", "s1", "user_input_requested", map[string]interface{}{
		"interactionId": "q-ms",
		"status":        "pending",
		"canRespond":    true,
		"canReject":     true,
		"questions": []interface{}{
			map[string]interface{}{
				"id":                 "q-ms",
				"header":             "测试多选",
				"prompt":             "西游记小故事.txt 已存在。请选择您想执行哪些操作：",
				"answerMode":         "multiple",
				"options":            []interface{}{map[string]interface{}{"id": "覆盖", "label": "覆盖"}},
				"allowsCustomAnswer": true,
				"required":           true,
			},
		},
	}))
	patch, ok := r.FlushPatch("dsh-web", "s1")
	if !ok {
		t.Fatal("expected user_input patch")
	}
	foundTurn := false
	for _, turn := range patch.UpsertTurns {
		if turn.TurnID == "T-new" {
			foundTurn = true
		}
	}
	if !foundTurn {
		t.Fatalf("UpsertTurns missing T-new: %+v", patch.UpsertTurns)
	}
	found := false
	for _, op := range patch.PartOps {
		if op.Op == "upsert_user_input" && op.TurnID == "T-new" && op.Part != nil && op.Part.UserInputInteractionID == "q-ms" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing upsert_user_input: %+v", patch.PartOps)
	}
	if patch.Execution == nil || patch.Execution.Phase != "requires_action" {
		t.Fatalf("execution = %+v, want requires_action", patch.Execution)
	}
}

func TestReducerQuestionAskedAfterPersistOnlyTurnStartedFlushesTurnShell(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T-new"}))
	if patch, ok := r.FlushPatch("dsh-web", "s1"); ok {
		t.Fatalf("turn_started must stay persist-only, got %+v", patch)
	}
	r.Apply(ev(2, "dsh-web", "s1", "question_asked", map[string]interface{}{
		"questionId":   "q-hulwa",
		"questionText": "文件已经创建过了。您现在想让我做什么？",
		"options": []interface{}{
			map[string]interface{}{"id": "overwrite", "label": "覆盖现有文件"},
		},
	}))
	patch, ok := r.FlushPatch("dsh-web", "s1")
	if !ok {
		t.Fatal("expected question patch")
	}
	foundTurn := false
	for _, turn := range patch.UpsertTurns {
		if turn.TurnID == "T-new" {
			foundTurn = true
		}
	}
	if !foundTurn {
		t.Fatalf("UpsertTurns missing T-new: %+v", patch.UpsertTurns)
	}
	foundInput := false
	for _, op := range patch.PartOps {
		if op.Op == "upsert_user_input" && op.TurnID == "T-new" && op.Part != nil && op.Part.UserInputInteractionID == "q-hulwa" {
			foundInput = true
		}
	}
	if !foundInput {
		t.Fatalf("missing upsert_user_input on T-new: %+v", patch.PartOps)
	}
}

func TestReducerPermissionResolvedDenyRejectsTool(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "dsh-web", "s1", "permission_request", map[string]interface{}{
		"requestId": "appr-bash",
		"toolName":  "bash",
	}))
	r.Apply(ev(3, "dsh-web", "s1", "permission_resolved", map[string]interface{}{
		"requestId": "appr-bash",
		"behavior":  "deny",
	}))

	proj, ok := r.Snapshot("dsh-web", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "appr-bash" {
			if p.RequiresPermissionConfirmation || p.ToolStatus != "rejected" {
				t.Fatalf("denied permission tool = %+v", p)
			}
			return
		}
	}
	t.Fatalf("missing denied tool part: %+v", proj.Turns[0].Assistant.Parts)
}
