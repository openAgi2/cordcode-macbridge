package codexremote

import (
	"encoding/json"
	"testing"
)

// 官方 thread/list ThreadStatus → RuntimeStateHint（ChatGPT 官方列表 spinner 的同源
// 信号；owner 2026-09-30 验收：Mac 端发起的执行中 session 不进 iOS 也要亮转圈）。
func TestMapCatalogThreadStatusHint(t *testing.T) {
	cases := []struct {
		name        string
		status      string
		want        string
		wantOutcome string
	}{
		{"active-no-flags", `{"type":"active","activeFlags":[]}`, "running", ""},
		{"active-null-flags", `{"type":"active"}`, "running", ""},
		{"active-waiting-approval", `{"type":"active","activeFlags":["waitingOnApproval"]}`, "requiresAction", ""},
		{"active-waiting-input", `{"type":"active","activeFlags":["waitingOnUserInput"]}`, "requiresAction", ""},
		{"idle", `{"type":"idle"}`, "", ""},
		{"not-loaded", `{"type":"notLoaded"}`, "", ""},
		{"system-error", `{"type":"systemError"}`, "", "failed"},
	}
	for _, tc := range cases {
		var row catalogThreadRow
		if err := json.Unmarshal([]byte(`{"id":"th1","name":"n","updatedAt":100,"cwd":"/tmp","status":`+tc.status+`}`), &row); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		info := mapCatalogThread(row)
		if info.RuntimeStateHint != tc.want {
			t.Fatalf("%s: hint = %q, want %q", tc.name, info.RuntimeStateHint, tc.want)
		}
		if info.OutcomeHint != tc.wantOutcome {
			t.Fatalf("%s: outcome hint = %q, want %q", tc.name, info.OutcomeHint, tc.wantOutcome)
		}
	}

	// status 字段缺席 → 空 hint。
	var row catalogThreadRow
	if err := json.Unmarshal([]byte(`{"id":"th1","name":"n","updatedAt":100,"cwd":"/tmp"}`), &row); err != nil {
		t.Fatalf("absent: unmarshal: %v", err)
	}
	if hint := mapCatalogThread(row).RuntimeStateHint; hint != "" {
		t.Fatalf("absent status: hint = %q, want empty", hint)
	}
	if hint := mapCatalogThread(row).OutcomeHint; hint != "" {
		t.Fatalf("absent status: outcome hint = %q, want empty", hint)
	}
}

// systemError → OutcomeHint=failed（红❗持久化，2026-10-01）＋ outcomeAt 复用行
// updatedAt（失败 turn 的最后活动时刻）——官方持久失败态跨 bridge 重启的来源。
func TestMapCatalogThreadSystemErrorOutcome(t *testing.T) {
	var row catalogThreadRow
	if err := json.Unmarshal([]byte(`{"id":"th1","name":"n","updatedAt":1790846818,"cwd":"/tmp","status":{"type":"systemError"}}`), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	info := mapCatalogThread(row)
	if info.OutcomeHint != "failed" {
		t.Fatalf("outcome hint = %q, want failed", info.OutcomeHint)
	}
	if info.RuntimeStateHint != "" {
		t.Fatalf("runtime hint = %q, want empty (systemError is not an execution state)", info.RuntimeStateHint)
	}
	if info.ModifiedAt.Unix() != 1790846818 {
		t.Fatalf("modifiedAt = %v, want 1790846818 (row updatedAt carries the outcome time)", info.ModifiedAt)
	}
}
