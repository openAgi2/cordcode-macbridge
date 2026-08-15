package dsh

// KNOWN_SESSION_EVENT_TYPES — frozen inventory of the 44 event-type names the
// pinned runtime recognizes (deepseek-harness@47f9438,
// packages/core/session/src/known-event-types.ts; repo copy:
// scripts/dsh-gate0/known-event-types.txt, verified by
// scripts/dsh-gate0/gen-known-event-types.py).
//
// Membership ONLY means "this build recognizes the name + schema". It is NOT
// "safe to ignore": a reader may skip an event ONLY when the envelope carries
// ignorable:true (§3.10.2). The set exists so violation diagnostics can
// distinguish "known but unimplemented by the driver" from "unknown to the
// pinned runtime" — both fail visibly either way.

var knownSessionEventTypes = map[string]bool{
	"agent-preset/selected":           true,
	"agent/inbox/spliced":             true,
	"approval/asked":                  true,
	"approval/decided":                true,
	"approval/policy":                 true,
	"assistant/chunk":                 true,
	"assistant/message":               true,
	"command/done":                    true,
	"command/run":                     true,
	"compaction/end":                  true,
	"compaction/prune":                true,
	"compaction/start":                true,
	"compaction/summary":              true,
	"feedback/record":                 true,
	"goal/change":                     true,
	"hook/invoked":                    true,
	"hook/result":                     true,
	"llm/retry":                       true,
	"llm/retry-started":               true,
	"permission/preset":               true,
	"plan/mode":                       true,
	"request/context":                 true,
	"request/header":                  true,
	"sandbox/mode":                    true,
	"schedule/change":                 true,
	"session/end-seed":                true,
	"session/title":                   true,
	"session/title-llm-request":       true,
	"step/end":                        true,
	"step/start":                      true,
	"subagent/descriptor":             true,
	"todo/write":                      true,
	"tool-workflow/agent-end":         true,
	"tool-workflow/agent-start":       true,
	"tool-workflow/run-end":           true,
	"tool-workflow/run-start":         true,
	"tool/call":                       true,
	"tool/code-dispatch":              true,
	"tool/code-dispatch-start":        true,
	"tool/result":                     true,
	"turn/end":                        true,
	"turn/start":                      true,
	"user/message":                    true,
	"web/deepseek-search-llm-request": true,
}
