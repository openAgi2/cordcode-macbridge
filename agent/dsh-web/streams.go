package dshweb

// Dual WebSocket downlink pump (design §4.3.1/§4.3.3): one mux stream
// (session events, ALL sessions — external turns included) + one host stream
// (session lifecycle → immediate catalog refresh signal). Official v1 has no
// `since`: reconnect = reopen stream + the bridge's history re-pull/forceCold
// reconciles (§8-5).
//
// Event routing (single-delivery rule):
//   - a session with a live bridge binding (StartSession'd) gets its frames
//     through that dshSession's Events() channel → relayEvents (registry,
//     kernel, conn targeting);
//   - every other session's frames go to the agent-level passive channel
//     (core.EventSubscriber → startPassiveSubscription broadcast) — external
//     turns stay visible without double delivery.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.EventSubscriber = (*Agent)(nil)

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

// passiveEvents returns (creating on demand) the passive channel.
func (a *Agent) passiveEvents() chan core.Event {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.passive == nil {
		a.passive = make(chan core.Event, 128)
	}
	return a.passive
}

// startStreams launches the pump once (idempotent).
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
	codecs := map[string]*sessionCodec{}
	go a.runStreamLoop(ctx, "mux", "/api/events.mux", a.dispatchMuxFrame, codecs)
	// Host frames share the codecs map is unnecessary; pass its own.
	go a.runStreamLoop(ctx, "host", "/api/events.host", a.dispatchHostFrame, map[string]*sessionCodec{})
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

// runStreamLoop keeps one downlink open with reconnect; every frame goes
// through dispatch. Reconnect is reopen-only (official v1: no since resume).
func (a *Agent) runStreamLoop(ctx context.Context, name, path string, dispatch func(context.Context, string, string, json.RawMessage), codecs map[string]*sessionCodec) {
	_ = codecs // per-session codecs are owned by the mux dispatch closure
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
		stream, err := client.OpenStream(ctx, name, path)
		if err != nil {
			slog.Info("dsh-web: stream dial failed, retrying", "stream", name, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(streamReconnectBackoff):
			}
			continue
		}
		slog.Info("dsh-web: stream open", "stream", name)
		for {
			frame, err := stream.Next(ctx)
			if err != nil {
				_ = stream.Close()
				if ctx.Err() != nil {
					return
				}
				slog.Info("dsh-web: stream ended, reopening", "stream", name, "error", err)
				break
			}
			if frame.Type != "server-request" {
				slog.Warn("dsh-web: unexpected frame envelope", "stream", name, "type", frame.Type)
				continue
			}
			dispatch(ctx, frame.RPCID, frame.Method, frame.Payload)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(streamReconnectBackoff):
		}
	}
}

// dispatchMuxFrame routes one mux ServerRequest payload.
func (a *Agent) dispatchMuxFrame(ctx context.Context, rpcID, method string, payload json.RawMessage) {
	switch method {
	case "session/event":
		var f struct {
			SessionID string           `json:"sessionId"`
			Event     sessionEventWire `json:"event"`
		}
		if err := json.Unmarshal(payload, &f); err != nil {
			slog.Warn("dsh-web: session/event frame unparsable", "error", err)
			return
		}
		feedWithReset(a.muxCodecs(), f.SessionID, &f.Event, func(events []core.Event) {
			a.deliverSessionEvents(f.SessionID, events)
		})

	case "session/subscribed":
		var f struct {
			SessionID string `json:"sessionId"`
			LastSeq   int64  `json:"lastSeq"`
		}
		if err := json.Unmarshal(payload, &f); err == nil {
			slog.Debug("dsh-web: subscribed", "sessionPrefix", shortLog(f.SessionID), "lastSeq", f.LastSeq)
		}

	case "approval/requested", "approval/resolved":
		a.handleApprovalFrame(ctx, rpcID, method, payload)

	case "question/requested", "question/resolved":
		a.handleQuestionFrame(ctx, rpcID, method, payload)

	case "session/queue", "session/jobs", "session/projection":
		// Renderer-side inbox/jobs/projection hints: no bridge timeline
		// mapping in phase 1 (todos/usage rides §4.3.8's phase-2 list).
		slog.Debug("dsh-web: mux frame noted", "method", method)

	case "stream/error":
		var f struct {
			Error RPCError `json:"error"`
		}
		if err := json.Unmarshal(payload, &f); err == nil {
			// 坑 7: the official stream failure text, verbatim in the log.
			slog.Warn("dsh-web: stream/error frame", "code", f.Error.Code, "message", f.Error.Message)
		}

	default:
		slog.Debug("dsh-web: unknown mux frame", "method", method)
	}
}

// dispatchHostFrame routes one host ServerRequest payload.
func (a *Agent) dispatchHostFrame(ctx context.Context, rpcID, method string, payload json.RawMessage) {
	switch method {
	case "host/session-added", "host/session-removed", "host/workspace-changed",
		"host/workspace-removed", "host/workspace-order-changed", "host/archived-sessions-changed":
		// 即时层: immediate catalog rescan → fingerprint diff → sessions_changed.
		a.signalRefresh()

	case "host/session-status":
		var f struct {
			SessionID string `json:"sessionId"`
			Running   bool   `json:"running"`
		}
		if err := json.Unmarshal(payload, &f); err != nil || f.SessionID == "" {
			return
		}
		a.running.setOne(f.SessionID, f.Running)
		// Badge recency rides the refreshed list (updatedAt bumps on turn
		// boundaries); the flip itself needs no synthetic timeline event.
		a.signalRefresh()

	case "host/agent-error":
		var f struct {
			SessionID string `json:"sessionId"`
			Message   string `json:"message"`
		}
		if err := json.Unmarshal(payload, &f); err == nil {
			slog.Warn("dsh-web: host/agent-error", "sessionPrefix", shortLog(f.SessionID), "message", f.Message)
		}

	case "stream/error":
		var f struct {
			Error RPCError `json:"error"`
		}
		if err := json.Unmarshal(payload, &f); err == nil {
			slog.Warn("dsh-web: host stream/error frame", "code", f.Error.Code, "message", f.Error.Message)
		}

	default:
		slog.Debug("dsh-web: unknown host frame", "method", method)
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
	if err := client.Call(listCtx, "session.list", sessionListRequest{}, &val); err != nil {
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
