// Package transcriptindex implements the boundary-safe transcript page index
// described in docs/2026-06-13-session-loading-systemic-redesign.md §6.1-6.4.
//
// A PageIndex records, per session transcript, only the locating metadata needed
// to rebuild a page of logical messages without parsing the whole file: each
// logical message's byte span (ReplayStart..EndOffset), its unclosed tool-use
// count, and a versioned prefix-generation lineage that lets backward cursors
// survive tail appends and Bridge restarts. It never caches message content, so
// it is a locator index, not a snapshot or a fake-data cache.
//
// The package is backend-agnostic: Codex and Claude differ in record shape and
// grouping rules, so each gets a SpanExtractor that mirrors the corresponding
// content grouping builder (agent/codex rich history and agent/claudecode rich
// history). Content rebuild over a page's byte union is delegated to a
// ContentReplayer supplied by the caller (the bridge wires it to the existing
// builders); matching back to spans is positional and fail-safe.
//
// The index is safe under appends (verified via a strong continuity anchor and
// file-identity check) and under crashes (atomic single-writer persistence with
// version/fingerprint/lineage validation; any damage triggers a full rebuild).
package transcriptindex
