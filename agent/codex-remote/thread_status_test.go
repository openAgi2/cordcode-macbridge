package codexremote

import (
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 官方 thread/status/changed → 控制面执行态投影（session-badges 上游对齐方案 §5.2
// 词表；官方 ThreadStatusChangedNotification 形状 thread.rs:1979-1983，部署版
// 0.159.0 已含）。SystemError/NotLoaded 不映射（诚实不冒充）。
func TestDecodeThreadStatusChanged(t *testing.T) {
	codec := NewLiveCodec()

	cases := []struct {
		name      string
		params    string
		wantCount int
		wantState string // "" = no event expected
	}{
		{"active-no-flags", `{"threadId":"th1","status":{"type":"active","activeFlags":[]}}`, 1, "running"},
		{"active-null-flags", `{"threadId":"th1","status":{"type":"active"}}`, 1, "running"},
		{"active-waiting-approval", `{"threadId":"th1","status":{"type":"active","activeFlags":["waitingOnApproval"]}}`, 1, "requiresAction"},
		{"active-waiting-input", `{"threadId":"th1","status":{"type":"active","activeFlags":["waitingOnUserInput"]}}`, 1, "requiresAction"},
		{"idle", `{"threadId":"th1","status":{"type":"idle"}}`, 1, "idle"},
		{"system-error-unmapped", `{"threadId":"th1","status":{"type":"systemError"}}`, 0, ""},
		{"not-loaded-unmapped", `{"threadId":"th1","status":{"type":"notLoaded"}}`, 0, ""},
		{"missing-thread-id", `{"status":{"type":"idle"}}`, 0, ""},
		{"missing-status", `{"threadId":"th1"}`, 0, ""},
	}
	for _, tc := range cases {
		events := codec.Decode(Notification{
			Method: "thread/status/changed",
			Params: json.RawMessage(tc.params),
		})
		if len(events) != tc.wantCount {
			t.Fatalf("%s: event count = %d, want %d (events=%+v)", tc.name, len(events), tc.wantCount, events)
		}
		if tc.wantCount == 0 {
			continue
		}
		ev := events[0]
		if ev.Type != core.EventSessionState {
			t.Fatalf("%s: type = %v, want EventSessionState", tc.name, ev.Type)
		}
		if ev.SessionID != "th1" {
			t.Fatalf("%s: sessionID = %q, want th1", tc.name, ev.SessionID)
		}
		if ev.SessionState == nil || ev.SessionState.State != tc.wantState {
			t.Fatalf("%s: state = %+v, want %q", tc.name, ev.SessionState, tc.wantState)
		}
	}
}
