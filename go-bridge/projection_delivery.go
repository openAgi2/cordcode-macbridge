package gobridge

import "log/slog"

// Session Projection Stream v2-marking (session_sync_v2). The projection_patch envelope
// construction + delivery live in event_publisher.go, which is the single blessed site for
// building a new business EventMessage (enforced by TestBusinessEventConstructionHasNoProductionBypass).
// See docs/protocol/bridge-v1.md「Session Projection Stream」and design §6.2.

// SetConnSyncV2 marks a connection as a session_sync_v2 client (it advertised session_sync_v2 in
// hello and the server enabled it). Called from the hello handlers. A v2 conn receives
// projection_patch frames in addition to raw events; the v2 client ignores raw content
// (design §9.3 dual-publish transition).
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
