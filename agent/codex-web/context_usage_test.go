package codexweb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestGetSessionContextUsageReadsOfficialThreadPathTokenCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread-events")
	contents := "{\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"info\":{\"total_token_usage\":{\"input_tokens\":24507448,\"cached_input_tokens\":23825280,\"output_tokens\":61725,\"reasoning_output_tokens\":14407,\"total_tokens\":24569173},\"last_token_usage\":{\"input_tokens\":124945,\"cached_input_tokens\":113408,\"output_tokens\":766,\"reasoning_output_tokens\":474,\"total_tokens\":125711},\"model_context_window\":258400}}}\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)
	readCalls := captureParams(s, "thread/read", map[string]any{
		"thread": map[string]any{"id": "thread-existing", "path": path},
	})
	a := New(nil)
	a.endpoint = &ServiceEndpoint{Source: SourceExternalDaemonReused, client: cl}

	usage, err := a.GetSessionContextUsage(context.Background(), "thread-existing")
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.UsedTokens != 125711 || usage.ContextWindow != 258400 {
		t.Fatalf("context occupancy = %+v", usage)
	}
	if usage.TotalTokens != 24569173 || usage.InputTokens != 124945 || usage.CachedInputTokens != 113408 {
		t.Fatalf("context accounting = %+v", usage)
	}
	expectParams(t, (*readCalls)[0], map[string]any{"threadId": "thread-existing"})

	// Every initial snapshot re-reads the official path so a Mac-side turn that
	// completed without an attached bridge listener cannot leave stale usage.
	if _, err := a.GetSessionContextUsage(context.Background(), "thread-existing"); err != nil {
		t.Fatal(err)
	}
	if len(*readCalls) != 2 {
		t.Fatalf("thread/read calls=%d, want 2", len(*readCalls))
	}
}

func TestAgentCachesOfficialContextUsageWithoutSessionListener(t *testing.T) {
	a := New(nil)
	a.dispatchEvent(core.Event{
		Type:      core.EventContextUsageUpdated,
		SessionID: "thread-passive",
		ContextUsage: &core.ContextUsage{
			UsedTokens: 73000, ContextWindow: 258400,
		},
	})
	usage := a.cachedContextUsage("thread-passive")
	if usage == nil || usage.UsedTokens != 73000 || usage.ContextWindow != 258400 {
		t.Fatalf("cached usage = %+v", usage)
	}
}
