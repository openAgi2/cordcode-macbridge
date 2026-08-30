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
func (a *Agent) ReadTurnItems(ctx context.Context, threadID, turnID string) ([]RemoteTurnItemEntry, error) {
	cl, err := a.paginatedClient()
	if err != nil {
		return nil, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, remoteTurnItemsDeadline)
	defer cancel()

	entries := make([]RemoteTurnItemEntry, 0, remoteItemsPageLimit)
	cursor := ""
	totalBytes := 0
	for page := 1; ; page++ {
		if page > remoteTurnItemsMaxPages {
			return nil, ErrTurnItemsMaxPages
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
		raw, rpcErr, err := cl.RequestContext(fetchCtx, "thread/items/list", params)
		if err != nil {
			if errors.Is(fetchCtx.Err(), context.DeadlineExceeded) {
				return nil, ErrTurnItemsTimeout
			}
			return nil, err
		}
		if rpcErr != nil {
			if errors.Is(fetchCtx.Err(), context.DeadlineExceeded) {
				return nil, ErrTurnItemsTimeout
			}
			return nil, rpcErr
		}
		totalBytes += len(raw)
		if totalBytes > remoteTurnItemsMaxBytes {
			return nil, ErrTurnItemsMaxBytes
		}
		var response remoteItemsListResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, fmt.Errorf("codex-remote: thread/items/list decode: %w", err)
		}
		for _, wire := range response.Data {
			if wire.TurnID != turnID {
				return nil, ErrForeignTurnItem
			}
			var probe struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if err := json.Unmarshal(wire.Item, &probe); err != nil {
				return nil, fmt.Errorf("%w: turn %s item decode: %v", ErrUnknownThreadItem, turnID, err)
			}
			if !remoteKnownItemTypes[probe.Type] {
				return nil, fmt.Errorf("%w: %q", ErrUnknownThreadItem, probe.Type)
			}
			entries = append(entries, RemoteTurnItemEntry{TurnID: wire.TurnID, Item: decodeRemoteThreadItem(wire.Item)})
		}
		if response.NextCursor == nil {
			return entries, nil
		}
		if *response.NextCursor == cursor {
			return nil, ErrRepeatedCursor
		}
		cursor = *response.NextCursor
	}
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
func (a *Agent) ReadColdHistory(ctx context.Context, threadID string) (*core.ColdHistoryResult, error) {
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
