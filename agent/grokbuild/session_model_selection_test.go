package grokbuild

// GetSessionModelSelection（core.SessionModelSelectionReader）：Grok 会话模型
// 真值只认 on-disk 证据（chat_history.jsonl 最新 assistant model_id，
// summary.json current_model_id 兜底）。无证据 → ok=false（「后端未提供
// 当前模型」，不造数）。路线图 §5.6 第 2/4 条 / Phase 1。

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func newTestAgentWithHome(t *testing.T, home string) *Agent {
	t.Helper()
	a, err := New(map[string]any{"grok_home": home, "cli_path": "true"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a.(*Agent)
}

func TestGetSessionModelSelection_LastAssistantModelIDWins(t *testing.T) {
	home := t.TempDir()
	sid := "019f-sel-session-aaaa"
	writeSessionFixture(t, home, "/tmp/proj", sid,
		map[string]any{
			"info":             map[string]any{"id": sid, "cwd": "/tmp/proj"},
			"current_model_id": "grok-4.5",
		},
		[]map[string]any{
			{"type": "user", "content": "hello"},
			{"type": "assistant", "content": "hi", "model_id": "grok-4"},
			{"type": "assistant", "content": "again", "model_id": "grok-4.6"},
		})

	a := newTestAgentWithHome(t, home)
	sel, ok := a.GetSessionModelSelection(context.Background(), sid)
	if !ok {
		t.Fatal("expected ok=true with transcript evidence")
	}
	if sel.Model != "grok-4.6" {
		t.Fatalf("Model = %q, want newest assistant model_id grok-4.6", sel.Model)
	}
	if sel.Provider != "default" {
		t.Fatalf("Provider = %q, want default (no custom provider)", sel.Provider)
	}
}

func TestGetSessionModelSelection_SummaryFallback(t *testing.T) {
	home := t.TempDir()
	sid := "019f-sel-session-bbbb"
	writeSessionFixture(t, home, "/tmp/proj", sid,
		map[string]any{
			"info":             map[string]any{"id": sid, "cwd": "/tmp/proj"},
			"current_model_id": "grok-4.6",
		},
		[]map[string]any{
			{"type": "user", "content": "hello"},
			{"type": "assistant", "content": "no model id on this row"},
		})

	a := newTestAgentWithHome(t, home)
	sel, ok := a.GetSessionModelSelection(context.Background(), sid)
	if !ok {
		t.Fatal("expected ok=true via summary.json current_model_id fallback")
	}
	if sel.Model != "grok-4.6" {
		t.Fatalf("Model = %q, want grok-4.6", sel.Model)
	}
}

func TestGetSessionModelSelection_NoEvidenceIsFalse(t *testing.T) {
	home := t.TempDir()
	sid := "019f-sel-session-cccc"
	writeSessionFixture(t, home, "/tmp/proj", sid,
		map[string]any{"info": map[string]any{"id": sid, "cwd": "/tmp/proj"}},
		[]map[string]any{{"type": "user", "content": "only a user row"}})

	a := newTestAgentWithHome(t, home)
	if _, ok := a.GetSessionModelSelection(context.Background(), sid); ok {
		t.Fatal("expected ok=false without model evidence (no fabricated model)")
	}
	// 完全不存在的 session 同样 ok=false。
	if _, ok := a.GetSessionModelSelection(context.Background(), "no-such-session"); ok {
		t.Fatal("expected ok=false for a missing session")
	}
}

func TestGetSessionModelSelection_CustomProviderNamespaced(t *testing.T) {
	home := t.TempDir()
	sid := "019f-sel-session-dddd"
	writeSessionFixture(t, home, "/tmp/proj", sid,
		map[string]any{"info": map[string]any{"id": sid, "cwd": "/tmp/proj"}},
		[]map[string]any{
			{"type": "user", "content": "hello"},
			{"type": "assistant", "content": "hi", "model_id": "glm-4.7"},
		})

	a := newTestAgentWithHome(t, home)
	a.SetProviders([]core.ProviderConfig{{Name: "glm", Models: []core.ModelOption{{Name: "glm-4.7"}}}})
	a.SetActiveProvider("glm")
	sel, ok := a.GetSessionModelSelection(context.Background(), sid)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if sel.Provider != "glm" {
		t.Fatalf("Provider = %q, want custom provider glm", sel.Provider)
	}
}
