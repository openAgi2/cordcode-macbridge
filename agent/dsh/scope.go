package dsh

// Notification session-scope router (design §3.8).
//
// The DSH SDK server broadcasts session.event / session.status /
// subagent.started / subagent.finished for EVERY session in the runtime —
// it does not scope to the SDK root session (server.ts:71-103). The driver is
// therefore responsible for client-side scoping (mirroring the SDK client's
// subscribeSessionTree): root frames enter the root codec, descendant frames
// are filtered, foreign frames are protocol pollution and terminate the
// process.
//
// Lineage tombstone: subagent.started records the edge; subagent.finished
// does NOT remove it — finished only ends the child's run, it is not a stream
// barrier, and deleting the edge would demote late child frames to foreign
// and kill the process wrongly (round8 P0-1). The set lives until process
// teardown.

import "log/slog"

// scopeClass classifies a notification's session id against the root tree.
type scopeClass int

const (
	scopeRoot scopeClass = iota
	scopeDescendant
	scopeForeign
)

// scopeRouter tracks the root session's lineage for one process lifetime.
// Owned by the read loop goroutine (same ownership as the codec).
type scopeRouter struct {
	rootID string
	// descendants is the tombstone set: every child ever announced via a
	// valid subagent.started. Never shrinks — finished keeps the edge.
	descendants map[string]bool
	// parents maps child → current parent edge (updated by a valid re-started).
	parents map[string]string
}

func newScopeRouter(rootID string) *scopeRouter {
	return &scopeRouter{
		rootID:      rootID,
		descendants: make(map[string]bool),
		parents:     make(map[string]string),
	}
}

// classify routes a session id: root, known descendant, or foreign.
func (r *scopeRouter) classify(sessionID string) scopeClass {
	switch {
	case sessionID == r.rootID:
		return scopeRoot
	case r.descendants[sessionID]:
		return scopeDescendant
	default:
		return scopeForeign
	}
}

// recordStarted applies one subagent.started edge with validation. It returns
// true when the edge was accepted (or was an idempotent duplicate); false
// when the notification was rejected — a foreign/self-looping/cyclic edge
// must not inject an arbitrary session into the root tree (§3.8).
//
// Rejection is not process-fatal: the edge simply is not created. A rejected
// child stays non-descendant, so any later frame it emits routes as foreign
// and terminates the process — pollution still fails visibly at the point it
// actually reaches the root stream.
func (r *scopeRouter) recordStarted(parent, child string) bool {
	if parent == "" || child == "" {
		slog.Warn("dsh: rejected subagent.started with empty ids",
			"parent_empty", parent == "", "child_empty", child == "")
		return false
	}
	if parent == child {
		slog.Warn("dsh: rejected self-loop subagent.started", "session", shortID(child))
		return false
	}
	if parent != r.rootID && !r.descendants[parent] {
		slog.Warn("dsh: rejected subagent.started with foreign parent (not injected into root tree)",
			"parent", shortID(parent), "child", shortID(child))
		return false
	}
	// Cycle guard: walking up from the new parent must not reach the child.
	for cur := parent; cur != r.rootID; {
		if cur == child {
			slog.Warn("dsh: rejected cyclic subagent.started", "session", shortID(child))
			return false
		}
		next, ok := r.parents[cur]
		if !ok {
			break
		}
		cur = next
	}
	// Idempotent duplicate / child-id reuse after finished: record (or
	// re-parent) the edge; the tombstone entry itself never regresses.
	r.parents[child] = parent
	r.descendants[child] = true
	return true
}

// recordFinished maintains the tombstone on subagent.finished: the edge is
// kept, only a diagnostic is recorded. lastAssistantMessage is optional by
// schema and never affects lineage.
func (r *scopeRouter) recordFinished(parent, child string) {
	if parent == "" || child == "" {
		slog.Warn("dsh: ignored subagent.finished with empty ids")
		return
	}
	if !r.descendants[child] {
		slog.Warn("dsh: subagent.finished for unknown child (missed/late started kept as diagnostic)",
			"child", shortID(child))
	}
	// Intentionally no deletion: tombstone persists until process teardown.
	slog.Debug("dsh: subagent finished — lineage tombstone retained",
		"child", shortID(child), "parent", shortID(parent))
}
