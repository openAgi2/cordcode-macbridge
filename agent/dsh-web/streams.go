package dshweb

// remote.mux live pump (typert gateway, design §4.3.1/§4.3.3 successor):
// ONE WebSocket (/api/remote.mux) carries every logical stream — the
// forwarded $events source (session lifecycle + approval/question
// waterfalls), the workspace/follow grouping baseline, and one session/follow
// per desired (bound or running) session. External turns stream live because
// api-session/activity (a human user/message) opens the follow on demand.
// The streams carry no resume cursor: reconnect = re-open + each follow's
// opening snapshot re-seeds its codec; the bridge's history re-pull/forceCold
// remains the reconcile (§8-5).
//
// Event routing (single-delivery rule):
//   - a session with a live bridge binding (StartSession'd) gets its events
//     through that dshSession's Events() channel → relayEvents (registry,
//     kernel, conn targeting);
//   - every other session's events go to the agent-level passive channel
//     (core.EventSubscriber → startPassiveSubscription broadcast) — external
//     turns stay visible without double delivery.

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.EventSubscriber = (*Agent)(nil)
var _ core.LiveEventSubscriber = (*Agent)(nil)

// streamReconnectBackoff is the delay between stream reopen attempts.
const streamReconnectBackoff = 2 * time.Second

// Subscribe implements core.EventSubscriber: the agent-level passive event
// channel (external sessions + control-plane flips), started lazily on first
// subscription. The channel is never closed while the agent lives; the pump
// drops events when no consumer keeps up (broadcast deltas are lossy-tolerant;
// hydrate/forceCold is the reconcile path).
func (a *Agent) Subscribe(ctx context.Context) (<-chan core.Event, error) {
	if _, err := a.clientFor(ctx); err != nil {
		return nil, err // instance unresolved: passive subscribe fails honestly
	}
	a.startStreams(ctx)
	return a.passiveEvents(), nil
}

type dshLiveTurnKey struct {
	SessionID string
	TurnID    string
}

type dshLiveTurnState struct {
	replay bool
}

// SubscribeLive implements the replay-free variant used by Web Push. DSH v1 has
// no stream cursor, so the raw passive channel cannot itself claim replay-free
// provenance. This filter admits a turn only when its lifecycle was observed on
// this subscription (turn/start, or orphan text adoption) and consumes each
// turn once. A repeated turn/start for the same still-active turn is treated as
// a reconnect replay: later text/completion for that turn is suppressed.
func (a *Agent) SubscribeLive(ctx context.Context) (<-chan core.Event, error) {
	events, err := a.Subscribe(ctx)
	if err != nil {
		return nil, err
	}
	out := make(chan core.Event, 128)
	go func() {
		active := make(map[dshLiveTurnKey]*dshLiveTurnState)
		completed := make(map[dshLiveTurnKey]struct{})
		var completedOrder []dshLiveTurnKey
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-events:
				key := dshLiveTurnKey{SessionID: ev.SessionID, TurnID: ev.TurnID}
				hasTurn := key.SessionID != "" && key.TurnID != ""
				_, wasCompleted := completed[key]
				switch ev.Type {
				case core.EventTurnStarted:
					if !hasTurn || wasCompleted {
						continue
					}
					if state := active[key]; state != nil {
						state.replay = true
						continue
					}
					active[key] = &dshLiveTurnState{}
				case core.EventText:
					if !hasTurn || wasCompleted {
						continue
					}
					state := active[key]
					if state == nil {
						state = &dshLiveTurnState{}
						active[key] = state
					}
					if state.replay {
						continue
					}
				case core.EventResult:
					if !hasTurn {
						continue
					}
					state := active[key]
					if state == nil || wasCompleted {
						continue
					}
					delete(active, key)
					completed[key] = struct{}{}
					completedOrder = append(completedOrder, key)
					if len(completedOrder) > 1024 {
						old := completedOrder[0]
						completedOrder = completedOrder[1:]
						delete(completed, old)
					}
					if state.replay {
						continue
					}
				default:
					// Non-turn control events are not replay candidates; retain the
					// existing passive subscription semantics for them.
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				default:
					// Web Push is a side effect, not the timeline source. Preserve the
					// passive channel's lossy-tolerant contract rather than blocking.
				}
			}
		}
	}()
	return out, nil
}

// passiveEvents returns (creating on demand) the passive channel.
func (a *Agent) passiveEvents() chan core.Event {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.passive == nil {
		a.passive = make(chan core.Event, 128)
	}
	return a.passive
}

// startStreams launches the remote.mux pump once (idempotent).
func (a *Agent) startStreams(ctx context.Context) {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.streamsStarted {
		return
	}
	a.streamsStarted = true
	if a.refreshSignals == nil {
		a.refreshSignals = make(chan struct{}, 16)
	}
	go a.runMuxLoop(ctx)
}

// CatalogRefreshSignals implements the bridge's refresh-signaler contract:
// host lifecycle frames poke the discovery worker for an immediate
// fingerprint rescan → sessions_changed (即时层, design §4.3.1).
func (a *Agent) CatalogRefreshSignals() <-chan struct{} {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.refreshSignals == nil {
		a.refreshSignals = make(chan struct{}, 16)
	}
	return a.refreshSignals
}

func (a *Agent) signalRefresh() {
	a.streamMu.Lock()
	ch := a.refreshSignals
	a.streamMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default: // a pending signal already covers this burst
	}
}

// ── remote.mux generation (gateway/src/{index.ts,stream-protocol.ts}) ────────
//
// One WebSocket carries every logical stream. Each generation opens:
//
//   - "$events"   — the forwarded Cordis event source: ready (binds this
//     generation's clientId), emit (api-session/* lifecycle), waterfall
//     (approval/request + user-questions/request — the §8-4 surface), and
//     cancel (a pending waterfall was withdrawn or claimed elsewhere).
//   - "ws"        — workspace/follow: one baseline (grouping truth for
//     ListSessions / project suggestions) then ordered increments.
//   - "f:<sid>"   — session/follow per DESIRED session (bound or running):
//     opening snapshot (recent records + projections) then gap-free journal
//     events — the successor of the pre-gateway agent-broadcast mux.
//
// Follows are demand-driven: api-session/activity (a human user/message was
// appended) opens a follow for that session, so external turns stream live
// without following the whole catalog (follow ACTIVATES a host Agent — the
// running+bound set stays small). Reconnect = new generation: every stream
// re-opens, the fresh $events ready frame rebinds the clientId, and each
// follow's opening snapshot re-seeds its codec (the official streams carry
// no resume cursor).

// muxStreamIDs name the fixed logical streams (per-session follows use
// followStreamID).
const (
	eventsStreamID = "events"
	wsStreamID     = "ws"
	remoteMuxPath  = "/api/remote.mux"
	// OD-4=A: the opening-snapshot window mirrors the official web client's
	// HISTORY_PAGE_OPTIONS (client/sessions/session.ts:54, served at :631) —
	// maxMessages 500 + turnWindow {minMessages: 50, minTurns: 2}. Gap-seed
	// depth only; cold pulls stay authoritative for full history.
	followMaxMsgs = 500
)

// followTurnWindow is the official window's turn boundary (session.ts:54).
var followTurnWindow = turnWindow{MinMessages: 50, MinTurns: 2}

func followStreamID(sessionID string) string { return "f:" + sessionID }

// remoteEventFrame is one $events stream value (stream-protocol.ts
// RemoteEventDownlinkFrame).
type remoteEventFrame struct {
	Type     string `json:"type"` // ready|emit|waterfall|cancel
	ClientID string `json:"clientId,omitempty"`
	Host     *struct {
		Home string `json:"home"`
	} `json:"host,omitempty"`
	Event   string            `json:"event,omitempty"`
	Args    []json.RawMessage `json:"args,omitempty"`
	EventID string            `json:"eventId,omitempty"`
	AgentID string            `json:"agentId,omitempty"`
	Request json.RawMessage   `json:"request,omitempty"`
}

// followRegistry tracks desired vs. opened per-session follows.
type followRegistry struct {
	mu      sync.Mutex
	desired map[string]bool
	opened  map[string]bool
}

func (f *followRegistry) ensure(sessionID string) bool { // returns true when newly desired
	if sessionID == "" {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.desired == nil {
		f.desired = map[string]bool{}
	}
	if f.desired[sessionID] {
		return false
	}
	f.desired[sessionID] = true
	return true
}

func (f *followRegistry) drop(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.desired, sessionID)
	delete(f.opened, sessionID)
}

func (f *followRegistry) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.desired))
	for id := range f.desired {
		out = append(out, id)
	}
	return out
}

// markOpened records that sessionID's follow went out on the live connection.
func (f *followRegistry) markOpened(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opened == nil {
		f.opened = map[string]bool{}
	}
	f.opened[sessionID] = true
}

// resetOpened clears the opened set at a generation boundary (desired stays).
func (f *followRegistry) resetOpened() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = map[string]bool{}
}

// ensureFollow registers a session for live following and opens its stream on
// the live connection when one exists.
func (a *Agent) ensureFollow(sessionID string) {
	if !a.follows.ensure(sessionID) {
		return
	}
	a.streamMu.Lock()
	stream := a.muxStream
	a.streamMu.Unlock()
	if stream == nil {
		return // the next generation's openLogicalStreams covers it
	}
	a.openFollow(sessionID, stream)
}

// openFollow sends the session/follow open for sessionID.
func (a *Agent) openFollow(sessionID string, stream *Stream) {
	max := followMaxMsgs
	window := followTurnWindow
	if err := stream.Send(muxClientMessage{
		Type:     "open",
		StreamID: followStreamID(sessionID),
		Endpoint: "session/follow",
		// The open payload is the endpoint's ordinary args object — the same
		// shape a unary Call would carry (wire.go's arg-name table).
		Payload: marshalOpenPayload(map[string]any{
			"args": map[string]any{
				"request": sessionFollowRequest{
					Address:         sessionAddress{Kind: "session", SessionID: sessionID},
					MaxMessages:     &max,
					TurnWindow:      &window,
					AssistantStream: true,
				},
			},
		}),
	}); err != nil {
		slog.Warn("dsh-web: session/follow open failed", "sessionPrefix", shortLog(sessionID), "error", err)
		return
	}
	a.follows.markOpened(sessionID)
}

// marshalOpenPayload marshals a stream-open payload; a marshal failure of
// plain map/struct values cannot happen, and the fallback keeps the send
// honest.
func marshalOpenPayload(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// runMuxLoop keeps one remote.mux generation open with reconnect.
func (a *Agent) runMuxLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		client, err := a.clientFor(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(streamReconnectBackoff):
			}
			continue
		}
		stream, err := client.OpenStream(ctx, "mux", remoteMuxPath)
		if err != nil {
			slog.Info("dsh-web: remote.mux dial failed, retrying", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(streamReconnectBackoff):
			}
			continue
		}
		slog.Info("dsh-web: remote.mux open")
		a.streamMu.Lock()
		a.muxStream = stream
		a.muxClientID = ""
		a.streamMu.Unlock()
		a.follows.resetOpened()
		if err := a.openLogicalStreams(stream); err != nil {
			slog.Info("dsh-web: logical stream open failed, reopening", "error", err)
			a.closeGeneration(stream)
			select {
			case <-ctx.Done():
				return
			case <-time.After(streamReconnectBackoff):
			}
			continue
		}
		err = a.drainMux(ctx, client, stream)
		a.closeGeneration(stream)
		if ctx.Err() != nil {
			return
		}
		slog.Info("dsh-web: remote.mux ended, reopening", "error", err)
		// The dead generation's clientId orphans every pending waterfall;
		// close their surfaced cards as settled-elsewhere.
		a.dropAllPendingInteractions()
		select {
		case <-ctx.Done():
			return
		case <-time.After(streamReconnectBackoff):
		}
	}
}

func (a *Agent) closeGeneration(stream *Stream) {
	a.streamMu.Lock()
	if a.muxStream == stream {
		a.muxStream = nil
	}
	a.streamMu.Unlock()
	_ = stream.Close()
}

// openLogicalStreams opens the fixed streams plus every desired follow on a
// fresh connection.
func (a *Agent) openLogicalStreams(stream *Stream) error {
	if err := stream.Send(muxClientMessage{
		Type:     "open",
		StreamID: eventsStreamID,
		Endpoint: "$events",
		Payload:  json.RawMessage(`{"args":{}}`),
	}); err != nil {
		return err
	}
	if err := stream.Send(muxClientMessage{
		Type:     "open",
		StreamID: wsStreamID,
		Endpoint: "workspace/follow",
		Payload:  json.RawMessage(`{"args":{}}`),
	}); err != nil {
		return err
	}
	for _, sid := range a.follows.snapshot() {
		a.openFollow(sid, stream)
	}
	return nil
}

// drainMux reads and dispatches frames until the socket errors.
func (a *Agent) drainMux(ctx context.Context, client *Client, stream *Stream) error {
	for {
		frame, err := stream.Next(ctx)
		if err != nil {
			return err
		}
		switch {
		case frame.Error != nil:
			slog.Warn("dsh-web: logical stream error frame",
				"stream", frame.StreamID, "code", frame.Error.Code, "message", frame.Error.Message)
			if sid, ok := strings.CutPrefix(frame.StreamID, "f:"); ok {
				// The follow is dead on this generation (unknown session,
				// withdrawn source). Drop the desire so reconnects stay clean.
				a.follows.drop(sid)
			}
			continue
		case frame.Type == "end":
			slog.Info("dsh-web: logical stream ended", "stream", frame.StreamID)
			if sid, ok := strings.CutPrefix(frame.StreamID, "f:"); ok {
				a.follows.drop(sid)
			}
			continue
		case frame.Type != "item":
			slog.Debug("dsh-web: unexpected mux frame type", "type", frame.Type, "stream", frame.StreamID)
			continue
		}
		switch frame.StreamID {
		case eventsStreamID:
			a.dispatchEventsFrame(client, frame.Value)
		case wsStreamID:
			a.dispatchWorkspaceFrame(frame.Value)
		default:
			if sid, ok := strings.CutPrefix(frame.StreamID, "f:"); ok {
				a.dispatchFollowItem(sid, frame.Value)
			} else {
				slog.Debug("dsh-web: unknown logical stream", "stream", frame.StreamID)
			}
		}
	}
}

// dispatchEventsFrame routes one $events value.
func (a *Agent) dispatchEventsFrame(client *Client, raw json.RawMessage) {
	var f remoteEventFrame
	if err := json.Unmarshal(raw, &f); err != nil {
		slog.Warn("dsh-web: $events frame unparsable", "error", err)
		return
	}
	switch f.Type {
	case "ready":
		a.streamMu.Lock()
		a.muxClientID = f.ClientID
		a.streamMu.Unlock()
		slog.Info("dsh-web: $events ready", "clientPrefix", shortLog(f.ClientID))
	case "emit":
		a.dispatchHostEmit(f.Event, f.Args)
	case "waterfall":
		a.dispatchWaterfall(client, f)
	case "cancel":
		a.closePendingInteraction(f.EventID, "cancelled")
	default:
		slog.Debug("dsh-web: unknown $events frame", "type", f.Type)
	}
}

// dispatchHostEmit routes the forwarded Cordis event allowlist
// (api/remotes/src/remote-events.ts API_REMOTE_FORWARDED_EVENTS).
func (a *Agent) dispatchHostEmit(event string, args []json.RawMessage) {
	argString := func(i int) string {
		if i >= len(args) {
			return ""
		}
		var s string
		if json.Unmarshal(args[i], &s) != nil {
			return ""
		}
		return s
	}
	argBool := func(i int) bool {
		if i >= len(args) {
			return false
		}
		var b bool
		if json.Unmarshal(args[i], &b) != nil {
			return false
		}
		return b
	}
	switch event {
	case "api-session/added":
		// 即时层: immediate catalog rescan → fingerprint diff → sessions_changed.
		a.signalRefresh()
	case "api-session/removed":
		a.follows.drop(argString(0))
		a.signalRefresh()
	case "api-session/status":
		sid := argString(0)
		running := argBool(1)
		a.running.setOne(sid, running)
		if running {
			a.ensureFollow(sid)
		} else if _, bound := a.bindings.get(sid); !bound {
			a.follows.drop(sid)
		}
		a.signalRefresh()
	case "api-session/activity":
		// A human user/message was appended — the turn that follows must
		// stream live even for sessions this bridge never held.
		a.ensureFollow(argString(0))
		a.signalRefresh()
	case "api-session/error":
		slog.Warn("dsh-web: api-session/error", "sessionPrefix", shortLog(argString(0)), "message", argString(1))
	case "goal/activation-changed", "agent-preset/selected", "commands/change",
		"llm/adapters-updated", "settings/document-updated",
		"credentials/reference-updated", "permission-presets/catalog-changed":
		// Catalog-affecting emits ride the same refresh signal (preset/mode
		// rows re-read on the next list).
		a.signalRefresh()
	default:
		slog.Debug("dsh-web: emit noted", "event", event)
	}
}

// dispatchFollowItem routes one session/follow item: the opening snapshot or
// one live journal event (both feed the same per-session codec; the codec's
// seq check dedups the snapshot/live overlap and resets on gaps). The opening
// snapshot is official history-page semantics — reconcile (seed) its settled
// records into codec state without re-broadcasting them as live activity;
// only the in-flight tail (records after the last turn/end) may be emitted,
// and only at/after the codec's already-delivered seq watermark.
func (a *Agent) dispatchFollowItem(sessionID string, raw json.RawMessage) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		slog.Warn("dsh-web: follow item unparsable", "sessionPrefix", shortLog(sessionID), "error", err)
		return
	}
	switch head.Type {
	case "snapshot":
		var snap followSnapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			slog.Warn("dsh-web: follow snapshot unparsable", "sessionPrefix", shortLog(sessionID), "error", err)
			return
		}
		// 2026-09-23 真机事故（owner：发送瞬间气泡聚拢、回复消失，回合落定后
		// 自愈）：开口快照的已落定历史被当作实时事件重播。旧 6 条窗口时只是
		// ≤6 个杂散事件；OD-4 对齐官方 HISTORY_PAGE_OPTIONS 500 条窗口后变成
		// 整段历史一次涌向 iOS（实测 18 回合 × 5 记录 = 90 个事件），运行中
		// 时间线被冲毁。官方 web 客户端把开口快照当历史页按 seq 幂等 reconcile
		// （client/sessions/session.ts:620-643 events.open(HISTORY_PAGE_OPTIONS)），
		// 不是新活动；桥的 iOS 事件通道是追加式，幂等必须在桥侧实现：
		//   • 全新 codec（priorNext=0，从未投递过）：已落定历史（最后一个
		//     turn/end 及之前）只播种 codec（水位 + 回合/消息状态），不发射
		//     ——iOS 经投影 hydrate 已持有该段；只有 turn/end 之后的在途尾部
		//     才发射（外部运行回合收养与本桥发送的占位/落定流都靠它）。
		//   • 重连（priorNext>0，codec 已投递到该水位）：发射所有 seq ≥
		//     priorNext 的记录——断线间隙新落账的记录（含已落定回合）从未
		//     到达过客户端，也不在旧 hydrate 里，是真正的增量；低于水位的
		//     记录已投递过，只播种。
		codecs := a.muxCodecs()
		priorNext := int64(0)
		if prior := codecs[sessionID]; prior != nil {
			priorNext = prior.expectedSeq
		}
		var lastSettledSeq int64 = -1
		for _, rec := range snap.Records {
			if rec.Type == "event" && rec.Event.Type == "turn/end" && rec.Event.Seq > lastSettledSeq {
				lastSettledSeq = rec.Event.Seq
			}
		}
		for _, rec := range snap.Records {
			if rec.Type != "event" {
				continue
			}
			env := rec.Event
			live := env.Seq >= priorNext && (env.Seq > lastSettledSeq || priorNext > 0)
			feedWithReset(codecs, sessionID, &env, func(events []core.Event) {
				if !live {
					return
				}
				a.deliverSessionEvents(sessionID, events)
			})
		}
		if usage := usageFromProjections(snap.Projections); usage != nil {
			a.rememberContextUsage(sessionID, usage)
			a.deliverSessionEvents(sessionID, []core.Event{{
				Type:         core.EventContextUsageUpdated,
				SessionID:    sessionID,
				ContextUsage: usage,
			}})
		}
		// Opted-in snapshots carry the live-attempt reconnect baseline
		// (official transport.ts:186-192 treats its absence as a protocol
		// error). Feed it after the records — official order: durable
		// window first, baseline expansion appended after.
		if snap.AssistantStream == nil {
			slog.Warn("dsh-web: follow snapshot missing opted-in assistantStream baseline",
				"sessionPrefix", shortLog(sessionID))
		} else if events := a.codecFor(sessionID).applyStreamBaseline(snap.AssistantStream); len(events) > 0 {
			a.deliverSessionEvents(sessionID, events)
		}
	case "event":
		var rec pageRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			slog.Warn("dsh-web: follow event unparsable", "sessionPrefix", shortLog(sessionID), "error", err)
			return
		}
		env := rec.Event
		feedWithReset(a.muxCodecs(), sessionID, &env, func(events []core.Event) {
			a.deliverSessionEvents(sessionID, events)
		})
	case "assistant-stream":
		// The typert generation's per-chunk live text carrier (journal commits
		// only at message completion — see codec.go's live-stream section).
		var wrap struct {
			Frame assistantStreamFrame `json:"frame"`
		}
		if err := json.Unmarshal(raw, &wrap); err != nil {
			slog.Warn("dsh-web: assistant-stream frame unparsable", "sessionPrefix", shortLog(sessionID), "error", err)
			return
		}
		if events := a.codecFor(sessionID).applyStreamFrame(&wrap.Frame); len(events) > 0 {
			a.deliverSessionEvents(sessionID, events)
		}
	default:
		slog.Debug("dsh-web: unknown follow item", "sessionPrefix", shortLog(sessionID), "type", head.Type)
	}
}

// dispatchWorkspaceFrame routes one workspace/follow item: the baseline sets
// the grouping cache; increments keep it current and poke the catalog
// refresh.
func (a *Agent) dispatchWorkspaceFrame(raw json.RawMessage) {
	var f workspaceFollowFrame
	if err := json.Unmarshal(raw, &f); err != nil {
		slog.Warn("dsh-web: workspace frame unparsable", "error", err)
		return
	}
	switch f.Type {
	case "baseline":
		if f.Value != nil {
			a.ws.applyBaseline(f.Value)
			a.signalRefresh()
		}
	case "upsert":
		if f.Workspace != nil {
			a.ws.applyUpsert(*f.Workspace)
			a.signalRefresh()
		}
	case "remove":
		a.ws.applyRemove(f.WorkspaceID)
		a.signalRefresh()
	case "order":
		a.ws.applyOrder(f.WorkspaceIDs)
	case "archived":
		a.ws.applyArchived(f.ArchivedSessionIDs)
		a.signalRefresh()
	case "pinned":
		// S5 (OD-1=A): the official pin set increment (complete set, official
		// order) — keeps the cached pin truth current for ListSessions rows
		// and list_pinned_sessions.
		a.ws.applyPinned(f.PinnedSessionIDs)
		a.signalRefresh()
	default:
		slog.Debug("dsh-web: unknown workspace frame", "type", f.Type)
	}
}

// muxCodecs lazily creates the pump-owned per-session codec map.
func (a *Agent) muxCodecs() map[string]*sessionCodec {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.codecs == nil {
		a.codecs = map[string]*sessionCodec{}
	}
	return a.codecs
}

// codecFor returns the session's codec, creating it on first touch (the
// snapshot baseline can arrive for a session whose records window is empty).
func (a *Agent) codecFor(sessionID string) *sessionCodec {
	codecs := a.muxCodecs()
	c := codecs[sessionID]
	if c == nil {
		c = newSessionCodec(sessionID)
		codecs[sessionID] = c
	}
	return c
}

// deliverSessionEvents applies the single-delivery rule.
func (a *Agent) deliverSessionEvents(sessionID string, events []core.Event) {
	if s, ok := a.bindings.get(sessionID); ok && s != nil {
		for _, ev := range events {
			ev.SessionID = sessionID
			s.emit(ev)
		}
		return
	}
	ch := a.passiveEvents()
	for _, ev := range events {
		ev.SessionID = sessionID
		select {
		case ch <- ev:
		default: // passive consumers are lossy-tolerant; hydrate reconciles
		}
	}
}

// ── SessionActivityProbing (§4.3.2 M1) ─────────────────────────────────────

// IsSessionActive reports whether a session currently has a turn in flight.
// Data source: the running cache (session.list rows + live host/session-status
// flips). Errors/unknown ⇒ conservative ACTIVE: a trailing unanswered turn
// must never be settled as dead while it may still be running (commit gate
// semantics, design §4.3.2).
func (a *Agent) IsSessionActive(ctx context.Context, sessionID string) bool {
	if running, known := a.running.get(sessionID); known {
		return running
	}
	// Unknown to the cache: refresh once (bounded); still unknown ⇒ active.
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := a.clientFor(listCtx)
	if err != nil {
		return true
	}
	var val sessionListValue
	if err := client.Call(listCtx, "session/list", listArgs(), &val); err != nil {
		return true
	}
	a.running.stage(val.Items)
	a.running.commit()
	if running, known := a.running.get(sessionID); known {
		return running
	}
	return true
}

var _ core.SessionActivityProbing = (*Agent)(nil)
