package opencodeweb

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// permissions.go implements the approval reply folding (design §3.4).
//
// bridge core.PermissionResult.Behavior only carries "allow" / "deny" — the
// official v2 enum is once | always | reject, and the 1.18 binary contains
// all three literals (S4 取证) but a binary's strings cannot prove the sole
// enumeration. Phase-1 strategy: PROBE first (1.18), fall back on 4xx, and
// record what worked into the diagnostics snapshot:
//
//	allow (incl. iOS Always label) → try once, on 4xx retry allow
//	deny                            → try reject, on 4xx retry deny
//
// v2 replies directly with once / reject. First answerer wins: a permission
// already answered by the web UI resolves server-side and our reply lands as
// a no-op error we surface as EventPermissionResolved-style success-best-effort.

// permissionFold records which literal the endpoint accepted, for diagnostics.
type permissionFold struct {
	mu      sync.Mutex
	lhits   map[string]string // "allow"|"deny" → accepted literal
	lprobes map[string][]string
}

var foldState = &permissionFold{
	lhits:   map[string]string{},
	lprobes: map[string][]string{},
}

func noteFoldAttempt(behavior, literal string, accepted bool) {
	foldState.mu.Lock()
	defer foldState.mu.Unlock()
	foldState.lprobes[behavior] = append(foldState.lprobes[behavior], literal)
	if accepted {
		foldState.lhits[behavior] = literal
	}
}

// foldDiagnostics renders the probe outcomes for run_diagnostics.
func foldDiagnostics() string {
	foldState.mu.Lock()
	defer foldState.mu.Unlock()
	var parts []string
	for _, behavior := range []string{"allow", "deny"} {
		if hit, ok := foldState.lhits[behavior]; ok {
			parts = append(parts, fmt.Sprintf("%s→%s", behavior, hit))
			continue
		}
		if tried := foldState.lprobes[behavior]; len(tried) > 0 {
			parts = append(parts, fmt.Sprintf("%s→no-literal-accepted(tried %s)", behavior, strings.Join(tried, ",")))
		}
	}
	if len(parts) == 0 {
		return "permission folding: no replies sent yet (probe strategy: once/reject first, allow/deny fallback)"
	}
	return "permission folding: " + strings.Join(parts, " ")
}

// replyLiteral returns the ordered literals to try for a bridge behavior on
// the 1.18 generation.
func replyLiteral(behavior string) []string {
	switch behavior {
	case "allow":
		return []string{"once", "allow"}
	case "deny":
		return []string{"reject", "deny"}
	default:
		return []string{"reject"}
	}
}

// respondPermission answers one permission request. It is shared by the
// active session surface and the agent-level SessionPermissionResponder
// (旁观 case, §8-6) — the serve holds the single answer lock, so first
// answerer wins server-side and a late reply is a harmless no-op error.
func (a *Agent) respondPermission(ctx context.Context, c *Client, sessionID, requestID string, result core.PermissionResult) error {
	if sessionID == "" || requestID == "" {
		return fmt.Errorf("opencode-web: permission reply needs session id and request id")
	}
	behavior := result.Behavior
	if behavior == "" {
		behavior = "deny"
	}

	if c.Generation() == generationV2 {
		literal := "reject"
		if behavior == "allow" {
			literal = "once"
		}
		body := map[string]any{"reply": literal}
		if result.Message != "" && behavior == "deny" {
			body["message"] = result.Message
		}
		path := c.apiPath("/session/") + sessionID + "/permission/" + requestID + "/reply"
		code, raw, err := c.doRequest(ctx, http.MethodPost, c.endpoint(path), body, a.GetWorkDir(), true)
		if err != nil {
			return fmt.Errorf("opencode-web permission reply: %w", err)
		}
		if code >= 300 {
			return fmt.Errorf("opencode-web permission reply HTTP %d: %s", code, truncateForError(string(raw)))
		}
		noteFoldAttempt(behavior, literal, true)
		return nil
	}

	for _, literal := range replyLiteral(behavior) {
		body := map[string]any{"response": literal}
		path := "/session/" + sessionID + "/permissions/" + requestID
		code, raw, err := c.doRequest(ctx, http.MethodPost, c.endpoint(path), body, a.GetWorkDir(), true)
		if err != nil {
			return fmt.Errorf("opencode-web permission reply: %w", err)
		}
		if code < 300 {
			noteFoldAttempt(behavior, literal, true)
			return nil
		}
		noteFoldAttempt(behavior, literal, false)
		slog.Debug("opencode-web: permission literal rejected", "session", sessionID, "literal", literal, "code", code, "body", truncateForError(string(raw)))
	}
	return fmt.Errorf("opencode-web permission reply: no accepted literal for behavior %q", behavior)
}

// RespondSessionPermission implements core.SessionPermissionResponder — the
// 旁观 path (Mac/web-initiated turn, no StartSession binding in the bridge).
func (a *Agent) RespondSessionPermission(ctx context.Context, sessionID, requestID string, result core.PermissionResult) error {
	c, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	return a.respondPermission(ctx, c, sessionID, requestID, result)
}

var _ core.SessionPermissionResponder = (*Agent)(nil)
