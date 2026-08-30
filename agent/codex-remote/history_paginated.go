package codexremote

// history_paginated.go is the lazy-history read layer (plan T1.1). It mirrors
// the official paginated primitives instead of the legacy
// thread/read(includeTurns=true) hydration: upstream tag
// rust-v0.150.0-alpha.12.2 thread_processor.rs:3222-3260 keeps that old path
// only "until those clients use thread/items/list" — and G0 proved that on
// paginated-history threads this app-server build never answers includeTurns
// over Remote Control at all (240s timeouts, attempts 009/010), while the
// legacy threads answer in ~768ms. ReadThreadSummary/ReadTurnItems below are
// therefore the only paginated-safe primitives; the full read stays available
// as an explicitly named legacy compatibility function (owner T0.5 ruling:
// only for historyMode=legacy, never as an automatic fallback).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Frozen resource gates (owner adjudication 2026-08-30). turnsPageLimit is
// owner-frozen; the remaining caps fail closed with distinct typed errors and
// may only be relaxed through real resource_limit-triggered data.
const (
	remoteTurnsPageLimit    = 30
	remoteItemsPageLimit    = 5
	remoteTurnItemsMaxPages = 24
	remoteTurnItemsMaxBytes = 512 * 1024
	// remoteTurnsMaxPages is a chain-safety cap (not owner-frozen): the
	// summary chain must terminate via nextCursor=nil EOF, and a runaway
	// chain fails explicitly instead of paginating forever.
	remoteTurnsMaxPages = 80
)

// remoteTurnItemsDeadline bounds the WHOLE per-turn items fetch (owner: "90s
// total deadline, not 24x30s"). Var only so tests can shrink it.
var remoteTurnItemsDeadline = 90 * time.Second

// resumeInitialTurnsPageVerifiedServers is the allowlist that anchors the
// owner-approved T0.6 candidate (G0 evidence report "T0.6 resume 候选路径裁
// 决", adjudication #2, 2026-08-30): thread/resume(excludeTurns:true +
// initialTurnsPage{limit:30, desc, summary}) answered 1 RPC 921ms/78611B vs
// the 2-RPC baseline 1336ms, thread.turns==[] asserted on both probe runs,
// single live attach, deterministic bytes. Keys are the codex app-server
// workspace version announced by the initialize response userAgent; the entry
// below is the probe-measured installed Desktop 26.825.41651 / codex-cli
// 0.151.0-alpha.7.1 (upstream diff to the plan-frozen tag additive-only).
// Every version NOT in this set — newer, older, unparseable, or not yet
// announced — pre-selects the official 2-RPC baseline for every attach
// (plan line 379: unverified versions must pre-select baseline). Add an
// entry only with a fresh probe + owner re-adjudication; a malformed
// candidate response on a listed version (thread.turns non-empty) still trips
// the per-process breaker (Agent.resumePageBroken) and every later attach
// pre-selects the plain excludeTurns form again.
var resumeInitialTurnsPageVerifiedServers = map[string]struct{}{
	"0.151.0-alpha.7.1": {},
}

// serverVersionFromUserAgent extracts the codex app-server workspace version
// from an initialize userAgent. codex-rs get_codex_user_agent formats it
// "{originator}/{workspace-version} ({os} {os-version}; {arch}) …" (login
// crate default_client.rs; every workspace crate shares the version), so the
// token after the last "/" of the first space-delimited segment is the
// server version. "" means unreadable — callers treat it as unverified.
func serverVersionFromUserAgent(userAgent string) string {
	segment := userAgent
	if idx := strings.IndexAny(segment, " \t"); idx >= 0 {
		segment = segment[:idx]
	}
	if idx := strings.LastIndex(segment, "/"); idx >= 0 {
		segment = segment[idx+1:]
	} else {
		return ""
	}
	return segment
}

// NoteServerUserAgent records the app-server version announced by the
// initialize response of the client epoch being bound (activateStream calls
// it right after a successful initialize). The value is client-epoch-scoped:
// BindClient clears it, so a version from a previous connection can never
// gate a new one, and attaches racing ahead of the initialize simply see an
// unverified server and pre-select the baseline.
func (a *Agent) NoteServerUserAgent(userAgent string) {
	version := serverVersionFromUserAgent(userAgent)
	a.mu.Lock()
	a.serverVersion = version
	a.mu.Unlock()
	if _, verified := resumeInitialTurnsPageVerifiedServers[version]; !verified {
		slog.Info("codex-remote: server version not probe-verified for resume initialTurnsPage — baseline reads pre-selected",
			"version", version, "userAgent", userAgent)
	}
}

// resumeInitialPage is the one round-trip cold-open artifact cached by the
// version-gated thread/resume candidate: the thread's authoritative
// historyMode (same Thread view thread/read returns) plus the first desc
// summary turns page (official TurnsPage shape rides initialTurnsPage, never
// thread.turns). Consumed at most once by ReadColdHistory; entries are
// client-epoch-scoped and cleared on BindClient.
type resumeInitialPage struct {
	client *Client
	mode   string
	page   RemoteTurnsPage
}

// remoteLegacyFullReadDeadline bounds the legacy compatibility full read
// (owner T0.5: full-read timeout must surface an explicit error).
var remoteLegacyFullReadDeadline = 90 * time.Second

var (
	// ErrTurnItemsMaxPages / ErrTurnItemsMaxBytes / ErrTurnItemsTimeout map
	// to the frozen reasonCodes max_pages / max_bytes / timeout at the
	// bridge ack layer (plan §3.2.0).
	ErrTurnItemsMaxPages = errors.New("codex-remote: turn items exceeded max_pages gate")
	ErrTurnItemsMaxBytes = errors.New("codex-remote: turn items exceeded max_bytes gate")
	ErrTurnItemsTimeout  = errors.New("codex-remote: turn items fetch exceeded total deadline")
	ErrTurnsMaxPages     = errors.New("codex-remote: summary chain exceeded page cap before EOF")
	// ErrRepeatedCursor mirrors the official repeated-cursor guard
	// (thread_processor.rs: a repeated store cursor is an internal error).
	ErrRepeatedCursor = errors.New("codex-remote: thread store returned a repeated cursor")
	// ErrUnknownThreadItem is the §2.2 atomic unknown-item failure for the
	// detail path: one undecodable item fails the whole turn fetch; summary
	// pages keep SkippedTypes diagnostics instead.
	ErrUnknownThreadItem = errors.New("codex-remote: unknown thread item variant in detail path")
	// ErrForeignTurnItem is the §3.0.7-3 turn-filter invariant: items/list
	// entries must belong to the requested turn.
	ErrForeignTurnItem = errors.New("codex-remote: items/list returned an entry for a foreign turn")
	// ErrUnknownHistoryMode fails closed when thread/read metadata does not
	// declare a known historyMode (never guess paginated or legacy).
	ErrUnknownHistoryMode = errors.New("codex-remote: unknown historyMode")
)

// remoteKnownItemTypes is the official ten-variant ThreadItem union at the
// frozen tag. Anything else (including the 0.151-alpha additive
// functionCallOutput variant) fails the detail path atomically until the
// decoder is extended with re-sampled evidence.
var remoteKnownItemTypes = map[string]bool{
	"userMessage":       true,
	"agentMessage":      true,
	"reasoning":         true,
	"commandExecution":  true,
	"fileChange":        true,
	"mcpToolCall":       true,
	"dynamicToolCall":   true,
	"plan":              true,
	"webSearch":         true,
	"contextCompaction": true,
}

// RemoteTurnsPage is one thread/turns/list page with the wire metadata the
// projection window needs (plan T1.2): network order is always the requested
// order, NextCursor=="" && EOF means no further pages upstream.
type RemoteTurnsPage struct {
	RequestedCursor string
	NextCursor      string
	BackwardsCursor string
	EOF             bool
	Turns           []remoteTurn
}

// RemoteThreadSummary is ReadThreadSummary's result. Meta is populated only
// on the first page (cursor == "") — older windows hydrate pages alone.
type RemoteThreadSummary struct {
	Meta *remoteThread
	Page RemoteTurnsPage
}

type remoteTurnsListResponse struct {
	Data            []remoteTurn `json:"data"`
	NextCursor      *string      `json:"nextCursor"`
	BackwardsCursor *string      `json:"backwardsCursor"`
}

type remoteThreadItemEntryWire struct {
	TurnID string          `json:"turnId"`
	Item   json.RawMessage `json:"item"`
}

type remoteItemsListResponse struct {
	Data            []remoteThreadItemEntryWire `json:"data"`
	NextCursor      *string                     `json:"nextCursor"`
	BackwardsCursor *string                     `json:"backwardsCursor"`
}

func (a *Agent) paginatedClient() (*Client, error) {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return nil, ErrNotConfigured
	}
	return cl, nil
}

// readThreadMeta fetches thread metadata without turns. thread/read omits
// includeTurns, which is the paginated-safe form (G0: includeTurns hangs on
// paginated threads over Remote Control).
func (a *Agent) readThreadMeta(ctx context.Context, threadID string) (*remoteThread, error) {
	cl, err := a.paginatedClient()
	if err != nil {
		return nil, err
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/read", map[string]any{"threadId": threadID})
	if err != nil {
		return nil, err
	}
	if rpcErr != nil {
		return nil, rpcErr
	}
	var response struct {
		Thread *remoteThread `json:"thread"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("codex-remote: thread/read decode: %w", err)
	}
	if response.Thread == nil || response.Thread.ID == "" {
		return nil, fmt.Errorf("codex-remote: thread/read missing thread identity")
	}
	return response.Thread, nil
}

// readTurnsPage fetches one thread/turns/list page (summary view, desc — the
// official paginated_thread_turns_list defaults this project freezes). It is
// the §2.4 producer hydration primitive: any cursor the upstream handed out
// can be resumed here.
func (a *Agent) readTurnsPage(ctx context.Context, threadID, cursor string) (RemoteTurnsPage, error) {
	cl, err := a.paginatedClient()
	if err != nil {
		return RemoteTurnsPage{}, err
	}
	params := map[string]any{
		"threadId":      threadID,
		"limit":         remoteTurnsPageLimit,
		"sortDirection": "desc",
		"itemsView":     remoteTurnItemsViewSummary,
	}
	if cursor != "" {
		params["cursor"] = cursor
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/turns/list", params)
	if err != nil {
		return RemoteTurnsPage{}, err
	}
	if rpcErr != nil {
		return RemoteTurnsPage{}, rpcErr
	}
	var response remoteTurnsListResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return RemoteTurnsPage{}, fmt.Errorf("codex-remote: thread/turns/list decode: %w", err)
	}
	page := RemoteTurnsPage{
		RequestedCursor: cursor,
		Turns:           response.Data,
		EOF:             response.NextCursor == nil,
	}
	if response.NextCursor != nil {
		page.NextCursor = *response.NextCursor
	}
	if response.BackwardsCursor != nil {
		page.BackwardsCursor = *response.BackwardsCursor
	}
	if cursor != "" && page.NextCursor == cursor {
		return RemoteTurnsPage{}, ErrRepeatedCursor
	}
	return page, nil
}

// ReadThreadSummary returns thread metadata (first page only) plus one
// summary-view turns page. Pass the previous page's NextCursor to walk older
// history; EOF on the page marks the upstream end.
func (a *Agent) ReadThreadSummary(ctx context.Context, threadID, cursor string) (*RemoteThreadSummary, error) {
	summary := &RemoteThreadSummary{}
	if cursor == "" {
		meta, err := a.readThreadMeta(ctx, threadID)
		if err != nil {
			return nil, err
		}
		summary.Meta = meta
	}
	page, err := a.readTurnsPage(ctx, threadID, cursor)
	if err != nil {
		return nil, err
	}
	summary.Page = page
	return summary, nil
}

// RemoteTurnItemEntry is one items/list entry with its owning turn.
type RemoteTurnItemEntry struct {
	TurnID string
	Item   remoteThreadItem
}

// ReadTurnItems pulls one turn's full items to EOF, mirroring the official
// paginated_turn_full_items invariants (thread_processor.rs:3222-3260):
// fixed turnId, Asc order, max page-size request, strict per-page
// deserialization, nextCursor=nil as the only EOF, repeated cursor fails
// immediately. On top it enforces the owner-frozen gates: at most 24 pages,
// at most 512KB of wire bytes, and a 90s total deadline — any breach fails
// the whole turn atomically (no truncation, no partial commit). Unknown item
// variants fail atomically per §2.2. An unknown-but-well-formed turnId is NOT
// an error: the official store filters and returns an empty page.
//
// Resource-gate metrics (owner adjudication 2026-08-30 #8): every exit —
// success or any failure — emits one slog summary with per-page counts so
// real 8–15-minute turns can re-adjudicate the gates with data. Content is
// NEVER logged, only sizes/counts/timings/gate. Projected (post-mapper)
// byte size is not measurable here — it rides the bridge patch layer and is
// observed when the incremental-commit design lands.
func (a *Agent) ReadTurnItems(ctx context.Context, threadID, turnID string) ([]RemoteTurnItemEntry, error) {
	cl, err := a.paginatedClient()
	if err != nil {
		return nil, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, remoteTurnItemsDeadline)
	defer cancel()

	fetchStart := time.Now()
	type pageStat struct {
		elapsedMs int64
		bytes     int
		items     int
	}
	var (
		pages          []pageStat
		rawBytes       int
		decodedBytes   int
		itemCount      int
		itemTypeCounts = map[string]int{}
		maxItemBytes   int
		maxItemType    string
		failGate       = "eof"
	)
	logMetrics := func(errOut error) {
		parts := make([]string, 0, len(pages))
		totalPageMs := int64(0)
		for _, p := range pages {
			totalPageMs += p.elapsedMs
			parts = append(parts, fmt.Sprintf("%dms/%dB/%di", p.elapsedMs, p.bytes, p.items))
		}
		slog.Info("codex-remote: turn items metrics",
			"threadId", threadID,
			"turnId", turnID,
			"pageCount", len(pages),
			"rawResponseBytes", rawBytes,
			"decodedItemBytes", decodedBytes,
			"itemCount", itemCount,
			"itemTypes", itemTypeCounts,
			"maxItemBytes", maxItemBytes,
			"maxItemType", maxItemType,
			"pageElapsedTotalMs", totalPageMs,
			"totalElapsedMs", time.Since(fetchStart).Milliseconds(),
			"pages", strings.Join(parts, " "),
			"failGate", failGate,
			"error", errText(errOut),
		)
	}

	entries := make([]RemoteTurnItemEntry, 0, remoteItemsPageLimit)
	cursor := ""
	for page := 1; ; page++ {
		if page > remoteTurnItemsMaxPages {
			failGate = "max_pages"
			err = ErrTurnItemsMaxPages
			logMetrics(err)
			return nil, err
		}
		params := map[string]any{
			"threadId":      threadID,
			"turnId":        turnID,
			"limit":         remoteItemsPageLimit,
			"sortDirection": "asc",
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		pageStart := time.Now()
		raw, rpcErr, err := cl.RequestContext(fetchCtx, "thread/items/list", params)
		pageElapsed := time.Since(pageStart)
		if err != nil {
			if errors.Is(fetchCtx.Err(), context.DeadlineExceeded) {
				failGate = "timeout"
				err = ErrTurnItemsTimeout
			} else {
				failGate = "rpc_error"
			}
			logMetrics(err)
			return nil, err
		}
		if rpcErr != nil {
			if errors.Is(fetchCtx.Err(), context.DeadlineExceeded) {
				failGate = "timeout"
				err = rpcErr
			} else {
				failGate = "rpc_rejected"
			}
			logMetrics(err)
			return nil, rpcErr
		}
		totalBytes := rawBytes + len(raw)
		if totalBytes > remoteTurnItemsMaxBytes {
			failGate = "max_bytes"
			rawBytes = totalBytes
			pages = append(pages, pageStat{elapsedMs: pageElapsed.Milliseconds(), bytes: len(raw), items: 0})
			err = ErrTurnItemsMaxBytes
			logMetrics(err)
			return nil, err
		}
		rawBytes = totalBytes
		var response remoteItemsListResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			failGate = "decode_error"
			logMetrics(err)
			return nil, fmt.Errorf("codex-remote: thread/items/list decode: %w", err)
		}
		for _, wire := range response.Data {
			if wire.TurnID != turnID {
				failGate = "foreign_turn_item"
				logMetrics(ErrForeignTurnItem)
				return nil, ErrForeignTurnItem
			}
			var probe struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if err := json.Unmarshal(wire.Item, &probe); err != nil {
				failGate = "unknown_item"
				logMetrics(err)
				return nil, fmt.Errorf("%w: turn %s item decode: %v", ErrUnknownThreadItem, turnID, err)
			}
			if !remoteKnownItemTypes[probe.Type] {
				failGate = "unknown_item"
				logMetrics(fmt.Errorf("%w: %q", ErrUnknownThreadItem, probe.Type))
				return nil, fmt.Errorf("%w: %q", ErrUnknownThreadItem, probe.Type)
			}
			decodedBytes += len(wire.Item)
			itemCount++
			itemTypeCounts[probe.Type]++
			if len(wire.Item) > maxItemBytes {
				maxItemBytes = len(wire.Item)
				maxItemType = probe.Type
			}
			entries = append(entries, RemoteTurnItemEntry{TurnID: wire.TurnID, Item: decodeRemoteThreadItem(wire.Item)})
		}
		pages = append(pages, pageStat{elapsedMs: pageElapsed.Milliseconds(), bytes: len(raw), items: len(response.Data)})
		if response.NextCursor == nil {
			logMetrics(nil)
			return entries, nil
		}
		if *response.NextCursor == cursor {
			failGate = "repeated_cursor"
			logMetrics(ErrRepeatedCursor)
			return nil, ErrRepeatedCursor
		}
		cursor = *response.NextCursor
	}
}

// errText keeps the metrics log free of full error chains while still naming
// the failure (typed errors above already carry the gate semantics).
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// readThreadFullCompat is the legacy full-read compatibility path
// (thread/read includeTurns=true). Owner T0.5: call ONLY for threads whose
// metadata declares historyMode=legacy; a timeout here is an explicit error
// and callers must never fall back or retry this as if it were paginated.
func (a *Agent) readThreadFullCompat(ctx context.Context, threadID string) (*remoteThread, error) {
	compatCtx, cancel := context.WithTimeout(ctx, remoteLegacyFullReadDeadline)
	defer cancel()
	thread, err := a.readThreadWithTurnsCtx(compatCtx, threadID, true)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("codex-remote: legacy full read exceeded %s deadline", remoteLegacyFullReadDeadline)
	}
	return thread, err
}

// GetTurnScopedRichHistory dispatches on the authoritative historyMode read
// from thread metadata (pre-selected dispatch: the paginated-safe reads come
// first and includeTurns is never attempted speculatively). paginated threads
// page summary turns and pull each turn's items through the official
// items/list loop; legacy threads use the compat full read; anything else
// fails closed.
func (a *Agent) GetTurnScopedRichHistory(ctx context.Context, sessionID string, limit int) ([]core.TurnScopedHistoryTurn, error) {
	meta, err := a.readThreadMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	switch meta.HistoryMode {
	case "legacy":
		thread, err := a.readThreadFullCompat(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		return mapRemoteHistoryTurns(thread, limit), nil
	case "paginated":
		return a.turnScopedHistoryPaginated(ctx, sessionID, limit)
	default:
		return nil, fmt.Errorf("%w: %q for thread %s", ErrUnknownHistoryMode, meta.HistoryMode, sessionID)
	}
}

// ReadUpstreamHistoryPage serves the bridge's lazy-history window producer
// (lazy-history §2.4 / bridge-v1.md R11a): exactly ONE bounded thread/turns/list
// page per call, keyed by the INTERNAL upstream cursor. Never full-reads. The
// returned page is ASCENDING (oldest→newest); NextCursor is upstream-owned and
// must never cross the bridge.
func (a *Agent) ReadUpstreamHistoryPage(ctx context.Context, sessionID, cursor string) (*core.UpstreamHistoryPage, error) {
	summary, err := a.ReadThreadSummary(ctx, sessionID, cursor)
	if err != nil {
		return nil, err
	}
	page := summary.Page
	turns := mapRemoteHistoryTurns(&remoteThread{ID: sessionID, Turns: page.Turns}, len(page.Turns))
	// network order is newest→oldest; reverse to ascending so the bridge prepends
	// in kernel order without re-sorting.
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return &core.UpstreamHistoryPage{Turns: turns, NextCursor: page.NextCursor}, nil
}

// turnScopedHistoryPaginated rebuilds the legacy API's turn list from the
// paginated primitives: collect desc summary pages until the caller's limit
// window or upstream EOF, reverse to thread order, then fetch every turn's
// detail items. Detail failures fail the whole call atomically — partial
// histories are never returned.
func (a *Agent) turnScopedHistoryPaginated(ctx context.Context, threadID string, limit int) ([]core.TurnScopedHistoryTurn, error) {
	desc := make([]remoteTurn, 0, remoteTurnsPageLimit)
	cursor := ""
	for page := 1; ; page++ {
		if page > remoteTurnsMaxPages {
			return nil, ErrTurnsMaxPages
		}
		pageResult, err := a.readTurnsPage(ctx, threadID, cursor)
		if err != nil {
			return nil, err
		}
		desc = append(desc, pageResult.Turns...)
		if pageResult.EOF || (limit > 0 && len(desc) >= limit) {
			break
		}
		cursor = pageResult.NextCursor
	}
	if limit > 0 && len(desc) > limit {
		desc = desc[:limit]
	}
	out := make([]core.TurnScopedHistoryTurn, 0, len(desc))
	for i := len(desc) - 1; i >= 0; i-- {
		turn := desc[i]
		entries, err := a.ReadTurnItems(ctx, threadID, turn.ID)
		if err != nil {
			return nil, err
		}
		historyTurn := mapRemoteTurnShell(turn)
		for _, entry := range entries {
			mapRemoteHistoryItem(&historyTurn, entry.Item)
		}
		out = append(out, historyTurn)
	}
	return out, nil
}

// ReadColdHistory is the mode-aware cold-open baseline (owner T0.5 ruling, T2.3):
// the AGENT owns the historyMode dispatch so the bridge never guesses and never
// auto-falls-back a legacy thread onto paginated reads. paginated → ONE summary
// page (asc) + upstream cursor fact; legacy → the explicit compat full read with
// an EOF cursor fact (legacy sessions never run the producer older-walk).
//
// T0.6 owner-approved fast path: when this connection's single thread/resume
// attach carried initialTurnsPage (resumeInitialTurnsPageVerified, version
// gated) the cached first page serves the paginated cold open with ZERO extra
// RPCs — the attach that the live relay performs anyway already paid for it.
// Every other case (no cached page, legacy/unknown mode in the cached meta,
// candidate breaker tripped) PRE-SELECTS the official baseline below:
// thread/read metadata + thread/turns/list — never a try-then-silent-full-read.
func (a *Agent) ReadColdHistory(ctx context.Context, threadID string) (*core.ColdHistoryResult, error) {
	if cached := a.takeResumeInitialPage(threadID); cached != nil && cached.mode == "paginated" {
		turns := mapRemoteHistoryTurns(&remoteThread{ID: threadID, Turns: cached.page.Turns}, len(cached.page.Turns))
		for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
			turns[i], turns[j] = turns[j], turns[i]
		}
		return &core.ColdHistoryResult{HistoryMode: "paginated", Page: &core.UpstreamHistoryPage{
			Turns: turns, NextCursor: cached.page.NextCursor,
		}}, nil
	}
	meta, err := a.readThreadMeta(ctx, threadID)
	if err != nil {
		return nil, err
	}
	switch meta.HistoryMode {
	case "paginated":
		page, err := a.readTurnsPage(ctx, threadID, "")
		if err != nil {
			return nil, err
		}
		turns := mapRemoteHistoryTurns(&remoteThread{ID: threadID, Turns: page.Turns}, len(page.Turns))
		for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
			turns[i], turns[j] = turns[j], turns[i]
		}
		return &core.ColdHistoryResult{HistoryMode: "paginated", Page: &core.UpstreamHistoryPage{
			Turns: turns, NextCursor: page.NextCursor,
		}}, nil
	case "legacy":
		turns, err := a.readThreadFullCompat(ctx, threadID)
		if err != nil {
			return nil, err
		}
		return &core.ColdHistoryResult{HistoryMode: "legacy", Page: &core.UpstreamHistoryPage{
			Turns: mapRemoteHistoryTurns(turns, 0), NextCursor: "",
		}}, nil
	default:
		return nil, ErrUnknownHistoryMode
	}
}

// ReadTurnDetail (T2.3): one turn's items to EOF under the frozen gates, mapped
// through the SAME mapRemoteHistoryItem discipline as the rich-history path
// (one mapper, one identity rule — official item ids stamped on parts). Decoding
// failures surface the agent's typed errors (ErrUnknownThreadItem etc.) for the
// bridge reasonCode mapping; SkippedTypes stays diagnostic — the bridge fails
// closed on a non-empty value.
func (a *Agent) ReadTurnDetail(ctx context.Context, sessionID, turnID string) (core.TurnScopedHistoryTurn, error) {
	entries, err := a.ReadTurnItems(ctx, sessionID, turnID)
	if err != nil {
		return core.TurnScopedHistoryTurn{}, err
	}
	turn := core.TurnScopedHistoryTurn{TurnID: turnID, Status: "completed"}
	for _, entry := range entries {
		mapRemoteHistoryItem(&turn, entry.Item)
	}
	return turn, nil
}

// takeResumeInitialPage consumes (at most once) the initial turns page cached
// by this client epoch's single thread/resume attach. Entries from a previous
// epoch, a non-paginated mode, or after the candidate breaker tripped are
// dropped — the caller then pre-selects the official 2-RPC baseline.
func (a *Agent) takeResumeInitialPage(threadID string) *resumeInitialPage {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.resumeInitialPages[threadID]
	if !ok {
		return nil
	}
	delete(a.resumeInitialPages, threadID)
	if entry.client != a.client || a.resumePageBroken || entry.mode == "" {
		return nil
	}
	return entry
}

// cacheResumeInitialPage stores the one-round-trip cold-open artifact from the
// version-gated resume candidate (caller holds the attach transaction, so at
// most one entry per thread per client epoch exists).
func (a *Agent) cacheResumeInitialPage(threadID string, entry *resumeInitialPage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != entry.client {
		return
	}
	if a.resumeInitialPages == nil {
		a.resumeInitialPages = map[string]*resumeInitialPage{}
	}
	a.resumeInitialPages[threadID] = entry
}

// resumeInitialPageCandidateOn reports whether the next attach should carry
// initialTurnsPage: the server version announced by THIS client epoch's
// initialize must be probe-verified (allowlist above) and the per-process
// breaker must not have tripped.
func (a *Agent) resumeInitialPageCandidateOn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.resumePageBroken {
		return false
	}
	_, verified := resumeInitialTurnsPageVerifiedServers[a.serverVersion]
	return verified
}

// breakResumeInitialPageCandidate trips the per-process breaker: a response
// shape that violates the probe contract (thread.turns non-empty = default
// full hydration) means the candidate is no longer trustworthy on this
// upstream; later attaches pre-select the plain excludeTurns form and cold
// opens use the official baseline. Never a silent fallback to a full read.
func (a *Agent) breakResumeInitialPageCandidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.resumePageBroken {
		a.resumePageBroken = true
		slog.Warn("codex-remote: resume initialTurnsPage candidate contract violated — reverting to official baseline reads")
	}
}
