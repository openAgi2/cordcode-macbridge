package gobridge

// transcriptStateProbe is a test-only observation point for the per-session
// transcript-state code path (findClaudeSessionFile / detectClaudeTranscriptState).
//
// The list-safe batch enricher enrichSessionStatesForList must never drive these
// functions for any listed row — that is the core CPU-fix invariant. List-path
// tests swap this variable for a counter and assert zero ticks, proving the list
// hot path opens zero transcript files. Production code leaves it at its no-op
// default; the call sites are cheap (a non-nil func variable).
//
// This is intentionally a package-level seam rather than dependency injection so
// the production list/enrichment code stays free of test plumbing.
var transcriptStateProbe = func() {}
