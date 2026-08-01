package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 这些测试覆盖 chatgpt-style-tool-activity-display 方案 Phase 0 的 Codex
// structured fileChange 样本（cordcode-ios docs/2026-08-01 §6.5 硬门）。它们锁定
// appServerFileChanges / appServerPatchChanges 对两种 wire 变体（list 与 path-keyed map）
// 的解析行为——这正是被 handlers_projection.go hydration 丢弃、需在 Phase 1A C-P0a 透传的
// structured FileChanges 的真实 wire 形态。
//
// A-form apply_patch 文本样本（*** Add/Update/Delete File）由 iOS 侧 patch parser 测试覆盖，
// 不在此处（macbridge 不从 apply_patch 文本产出 structured FileChanges）。

// applyPatchFixturesDir 返回脱敏 apply_patch/fileChange 样本目录。
func applyPatchFixturesDir() string {
	return filepath.Join("testdata", "tool-apply-patch")
}

// loadApplyPatchEnvelope 加载 structured fixture 的顶层 rpcNotificationEnvelope。
func loadApplyPatchEnvelope(t *testing.T, name string) (method string, params json.RawMessage) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(applyPatchFixturesDir(), name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var envelope rpcNotificationEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return envelope.Method, envelope.Params
}

// extractChangesFromItemParams 从 item/{started,completed} 的 params 里取出 item 再取 changes。
func extractChangesFromItemParams(t *testing.T, params json.RawMessage) any {
	t.Helper()
	var p struct {
		Item map[string]any `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	return p.Item["changes"]
}

func TestApplyPatchFixtures_ListVariant(t *testing.T) {
	_, params := loadApplyPatchEnvelope(t, "filechange_list_variant.json")
	changes := appServerFileChanges(extractChangesFromItemParams(t, params))
	if len(changes) != 2 {
		t.Fatalf("list variant: expected 2 changes, got %d", len(changes))
	}
	// appServerFileChanges 保留入参顺序
	if changes[0].Path != "src/models/UserProfile.swift" || changes[0].Kind != "edit" {
		t.Errorf("change[0] = %+v, want UserProfile edit", changes[0])
	}
	if changes[1].Path != "src/services/UserService.swift" || changes[1].Kind != "create" {
		t.Errorf("change[1] = %+v, want UserService create", changes[1])
	}
	if changes[0].Diff == "" {
		t.Error("change[0] diff should be preserved, got empty")
	}
}

func TestApplyPatchFixtures_MapVariant(t *testing.T) {
	_, params := loadApplyPatchEnvelope(t, "filechange_map_variant.json")
	changes := appServerPatchChanges(extractChangesFromItemParams(t, params))
	if len(changes) != 2 {
		t.Fatalf("map variant: expected 2 changes, got %d", len(changes))
	}
	// map 变体顺序不稳定，按 path 排序后断言
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	// src/config/Settings.swift (type=update -> edit)
	if changes[0].Path != "src/config/Settings.swift" || changes[0].Kind != "edit" {
		t.Errorf("map change[0] = %+v, want Settings edit (type=update normalized)", changes[0])
	}
	// src/utils/Logger.swift (type=add)。Phase 0 characterization：当前 appServerPatchChanges
	// 只把 update→edit 归一，add 透传为 "add"（未归一 create）。这是 Phase 1A C-P0a 须修的真实缺口：
	// add/create 必须归一为 create，否则 iOS 拿到的 kind 不是 create 动词。修复后翻转此断言。
	if changes[1].Path != "src/utils/Logger.swift" {
		t.Errorf("map change[1] path = %q, want src/utils/Logger.swift", changes[1].Path)
	}
	if changes[1].Kind != "add" {
		t.Errorf("map change[1] kind = %q, want \"add\" (characterization: add not yet normalized to create; Phase 1A fix will flip to create)", changes[1].Kind)
	}
	// unified_diff 字段应进入 Diff
	if changes[0].Diff == "" {
		t.Error("map change[0] unified_diff should be preserved as Diff, got empty")
	}
}

// TestApplyPatchFixtures_NotificationRoundTrip 验证 list 变体 fixture 经完整
// handleNotification 路径后，tool_use 事件携带 structured FileChanges（这是 Phase 1A
// hydration 透传的 live 基线——事件层已产出 FileChanges，仅 hydration 层丢弃）。
func TestApplyPatchFixtures_NotificationRoundTrip(t *testing.T) {
	coll := runNotificationFromSubdir(t, "tool-apply-patch", "filechange_list_variant.json")
	if len(coll.events) == 0 {
		t.Fatal("expected at least 1 event, got 0")
	}
	var toolUse *core.Event
	for i := range coll.events {
		if coll.events[i].Type == core.EventToolUse {
			toolUse = &coll.events[i]
			break
		}
	}
	if toolUse == nil {
		t.Fatalf("no EventToolUse emitted; events=%v", eventTypes(coll.events))
	}
	if toolUse.ToolName != "Patch" {
		t.Errorf("tool name = %q, want Patch", toolUse.ToolName)
	}
	if len(toolUse.FileChanges) != 2 {
		t.Errorf("EventToolUse FileChanges len = %d, want 2 (structured must survive to event layer)", len(toolUse.FileChanges))
	}
}

// runNotificationFromSubdir 同 runNotification，但 fixture 位于 testdata 子目录。
func runNotificationFromSubdir(t *testing.T, subdir, name string) *mockCollector {
	t.Helper()
	s := &appServerSession{
		events:  make(chan core.Event, 128),
		pending: make(map[int64]chan rpcResponseEnvelope),
	}
	s.alive.Store(true)
	s.threadID.Store("thread_test")

	data, err := os.ReadFile(filepath.Join("testdata", subdir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var envelope rpcNotificationEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	done := make(chan struct{})
	coll := newMockCollector()
	go func() {
		defer close(done)
		for ev := range s.events {
			coll.collect(ev)
		}
	}()
	s.handleNotification(envelope.Method, envelope.Params)
	close(s.events)
	<-done
	return coll
}

func eventTypes(events []core.Event) []core.EventType {
	out := make([]core.EventType, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}
