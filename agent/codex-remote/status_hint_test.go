package codexremote

import (
	"encoding/json"
	"testing"
)

// 官方 thread/list ThreadStatus → RuntimeStateHint（ChatGPT 官方列表 spinner 的同源
// 信号；owner 2026-09-30 验收：Mac 端发起的执行中 session 不进 iOS 也要亮转圈）。
func TestMapCatalogThreadStatusHint(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   string
	}{
		{"active-no-flags", `{"type":"active","activeFlags":[]}`, "running"},
		{"active-null-flags", `{"type":"active"}`, "running"},
		{"active-waiting-approval", `{"type":"active","activeFlags":["waitingOnApproval"]}`, "requiresAction"},
		{"active-waiting-input", `{"type":"active","activeFlags":["waitingOnUserInput"]}`, "requiresAction"},
		{"idle", `{"type":"idle"}`, ""},
		{"not-loaded", `{"type":"notLoaded"}`, ""},
		{"system-error", `{"type":"systemError"}`, ""},
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
	}

	// status 字段缺席 → 空 hint。
	var row catalogThreadRow
	if err := json.Unmarshal([]byte(`{"id":"th1","name":"n","updatedAt":100,"cwd":"/tmp"}`), &row); err != nil {
		t.Fatalf("absent: unmarshal: %v", err)
	}
	if hint := mapCatalogThread(row).RuntimeStateHint; hint != "" {
		t.Fatalf("absent status: hint = %q, want empty", hint)
	}
}
