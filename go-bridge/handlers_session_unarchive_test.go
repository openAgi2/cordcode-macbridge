package gobridge

// handlers_session_unarchive_test.go — S5（OD-1=A）unarchive_session RPC：
// capability 只随 SessionUnarchiver 广告；非实现者 not_supported；成功回
// session 行；官方错误 verbatim（unarchive_failed 携原文）。

import (
	"context"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSessionUnarchiveCapabilityAdvertisement(t *testing.T) {
	found := false
	for _, c := range deriveBackendCapabilities("dsh-web", &unarchiverAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}, "") {
		if c == "session_unarchive" {
			found = true
		}
	}
	if !found {
		t.Fatal("dsh-web (SessionUnarchiver) must advertise session_unarchive")
	}
	for _, id := range []string{"grok", "claude", "codex-remote", "opencode-web"} {
		for _, c := range deriveBackendCapabilities(id, &fakeAgent{name: id}, "") {
			if c == "session_unarchive" {
				t.Fatalf("%s must NOT advertise session_unarchive (no SessionUnarchiver)", id)
			}
		}
	}
}

type unarchiverAgent struct {
	*fakeAgent
}

func (a *unarchiverAgent) UnarchiveSession(_ context.Context, _ string) (*core.AgentSessionInfo, error) {
	return &core.AgentSessionInfo{ID: "s1"}, nil
}

func TestHandleUnarchiveSession(t *testing.T) {
	h := NewHandlersWithContext(context.Background())

	// 非 SessionUnarchiver backend → not_supported。
	conn := newCaptureConn()
	h.handleUnarchiveSession(conn, WireMessage{RequestID: "r1",
		Params: mustJSON(t, map[string]string{"sessionId": "s1"})}, &fakeAgent{name: "grok"})
	if conn.lastErrCode != "not_supported" {
		t.Fatalf("non-unarchiver must get not_supported, got %q", conn.lastErrCode)
	}

	// 成功：返回恢复行（ArchivedAt 零）。
	conn2 := newCaptureConn()
	h.handleUnarchiveSession(conn2, WireMessage{RequestID: "r2",
		Params: mustJSON(t, map[string]string{"sessionId": "s1"})}, &unarchiverAgent{fakeAgent: &fakeAgent{name: "dsh-web"}})
	if conn2.lastErrCode != "" {
		t.Fatalf("unarchive failed: %q", conn2.lastErrCode)
	}
	if !strings.Contains(string(conn2.lastResultJSON), `"id":"s1"`) {
		t.Fatalf("result = %s, want the restored session row", conn2.lastResultJSON)
	}
	if strings.Contains(string(conn2.lastResultJSON), "archivedAtMillis") {
		t.Fatalf("restored row must not carry archivedAtMillis: %s", conn2.lastResultJSON)
	}

	// 空 sessionId → missing_param。
	conn3 := newCaptureConn()
	h.handleUnarchiveSession(conn3, WireMessage{RequestID: "r3",
		Params: mustJSON(t, map[string]string{"sessionId": " "})}, &unarchiverAgent{fakeAgent: &fakeAgent{name: "dsh-web"}})
	if conn3.lastErrCode != "missing_param" {
		t.Fatalf("empty sessionId must get missing_param, got %q", conn3.lastErrCode)
	}
}
