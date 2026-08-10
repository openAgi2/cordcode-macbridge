package gobridge

import "log/slog"

// Session Projection Stream v2-marking (session_sync_v2). The projection_patch envelope
// construction + delivery live in event_publisher.go, which is the single blessed site for
// building a new business EventMessage (enforced by TestBusinessEventConstructionHasNoProductionBypass).
// See docs/protocol/bridge-v1.md「Session Projection Stream」and design §6.2.

// SetConnSyncV2 marks a connection as a session_sync_v2 client (it advertised session_sync_v2 in
// hello and the server enabled it). Called from the hello handlers. Since Phase 4, this capability
// is an unambiguous projection-only ownership promise: timeline-semantic raw events are filtered
// for this connection while projection frames and non-timeline control-plane events remain.
func (p *EventPublisher) SetConnSyncV2(conn Connection, enabled bool) {
	if p == nil || conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	remote := conn.RemoteAddr()
	device := ""
	if d := conn.AuthedDevice(); d != nil {
		device = d.DeviceID
	}
	if enabled {
		p.syncV2[conn] = true
	} else {
		delete(p.syncV2, conn)
	}
	slog.Info("go-bridge: [K4Patch] syncV2_mark",
		"remote", remote, "device", device,
		"enabled", enabled, "syncV2Size", len(p.syncV2),
	)
}

// isSessionSyncV2RawTimelineEvent mirrors the content-writer seals in iOS and remote-web.
// These events have already been reduced into SessionProjection before delivery, so sending the
// raw frame to a projection-only client would recreate the retired dual-publish path.
//
// Keep this an explicit deny-list rather than treating every session-scoped event as content:
// todos/context/catalog/diagnostic controls are not all represented by SessionProjection yet.
func isSessionSyncV2RawTimelineEvent(event string) bool {
	switch event {
	case "turn_started", "turn_completed",
		"user_message", "system_message",
		"text_delta", "message_updated", "message_content",
		"reasoning_delta", "thinking_delta",
		"tool_started", "tool_finished", "tool_content",
		"permission_request", "permission_asked",
		"question_asked", "question_resolved",
		"context_compressing", "context_compressed",
		"session_state_changed", "session_running_signal",
		"delivery_reconcile_required",
		"error":
		return true
	default:
		return false
	}
}

// Canonical structured-input events are projection-only for every connection. Legacy clients
// receive the one-way question_asked/question_resolved presentation emitted by the adapter after
// the canonical event has entered the reducer; v2 clients receive only the resulting projection.
func isProjectionOnlyCanonicalEvent(event string) bool {
	switch event {
	case "user_input_requested", "user_input_resolved":
		return true
	default:
		return false
	}
}

// Legacy question frames are a presentation derived from canonical user_input state. They may
// be delivered to legacy clients, but must never be ingested back into the projection Kernel.
func isDerivedLegacyQuestionEvent(event string) bool {
	return event == "question_asked" || event == "question_resolved"
}

// shouldDeliverRawEventLocked reports whether conn remains eligible for the raw envelope.
// Sessionless errors/control notifications are never projection-owned even if they reuse an event
// name from the timeline deny-list. Caller must hold p.mu: syncV2 is mutable connection state.
func (p *EventPublisher) shouldDeliverRawEventLocked(conn Connection, backendID, sessionID, event string) bool {
	if backendID != "" && sessionID != "" && isProjectionOnlyCanonicalEvent(event) {
		return false
	}
	return !p.syncV2[conn] || backendID == "" || sessionID == "" || !isSessionSyncV2RawTimelineEvent(event)
}

// ConnSyncV2 reports the capability negotiated for this exact logical connection.
// Rebind/recovery code may restore subscriptions, but must never manufacture this
// ownership bit: only a successful hello negotiation may enable it.
func (p *EventPublisher) ConnSyncV2(conn Connection) bool {
	if p == nil || conn == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.syncV2[conn]
}

// SetConnReadFileV2 records the authenticated hello negotiation for this exact connection.
// Replacement connections must negotiate again; UnregisterConnection clears the mark.
func (p *EventPublisher) SetConnReadFileV2(conn Connection, enabled bool) {
	if p == nil || conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if enabled {
		p.readFileV2[conn] = true
	} else {
		delete(p.readFileV2, conn)
	}
}

func (p *EventPublisher) ConnReadFileV2(conn Connection) bool {
	if p == nil || conn == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readFileV2[conn]
}

// SetConnCatalogCursorEpochV2 records that the client advertised catalog_cursor_epoch_v2 in
// hello (cross-backend session catalog parity §4.1.1 / §10). Only connections marked here may
// receive v2 (epoch-bearing) list_sessions cursors and cursor_stale; every other connection
// keeps the legacy v1 cursor path byte-for-byte. This is a pure client-capability gate (no
// server-side toggle): the capability declaration IS the §10 release gate. Replacement
// connections must negotiate again; UnregisterConnection clears the mark.
func (p *EventPublisher) SetConnCatalogCursorEpochV2(conn Connection, enabled bool) {
	if p == nil || conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if enabled {
		p.catalogCursorEpochV2[conn] = true
	} else {
		delete(p.catalogCursorEpochV2, conn)
	}
}

// ConnCatalogCursorEpochV2 reports whether this exact connection negotiated catalog_cursor_epoch_v2.
// The list handler (ocHandleListSessions) consults it to decide v2-snapshot path vs legacy v1.
func (p *EventPublisher) ConnCatalogCursorEpochV2(conn Connection) bool {
	if p == nil || conn == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.catalogCursorEpochV2[conn]
}
