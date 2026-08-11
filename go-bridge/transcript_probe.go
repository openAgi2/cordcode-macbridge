package gobridge

// noteTranscriptStateProbe is a handler-local test observation point for the per-session
// transcript-state code path (findClaudeSessionFile / detectClaudeTranscriptState).
//
// The list-safe batch enricher enrichSessionStatesForList must never drive these
// functions for any listed row — that is the core CPU-fix invariant. List-path
// tests replace the callback on their own Handlers instance and assert zero ticks,
// proving the list hot path opens zero transcript files without cross-test leakage.
func (h *Handlers) noteTranscriptStateProbe() {
	if h != nil && h.transcriptStateProbe != nil {
		h.transcriptStateProbe()
	}
}
