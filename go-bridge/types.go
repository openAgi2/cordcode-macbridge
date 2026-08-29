package gobridge

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// WireMessage is the top-level envelope for all WS messages.
type WireMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId,omitempty"`
	BackendID string `json:"backendId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Method    string `json:"method,omitempty"`
	Operation string `json:"operation,omitempty"`
	Event     string `json:"event,omitempty"`
	// BulkCorrelationID 是 R1.4（§3.6.4）read_file_v2 的 request-aware progress correlation，
	// 由 client 在 writer commit 前预绑定，放在加密 inner RPC envelope 顶层（与 method/requestId/params
	// 同级）。仅在当前 attempt 走 Relay 且 client 已 ack relay_chunks_v1 + relay_chunk_progress_v1 时存在。
	// Direct / 其他 RPC / 非 RPC event 一律不得携带。allowlist 本期只有 read_file_v2。
	BulkCorrelationID       string              `json:"bulkCorrelationId,omitempty"`
	Params                  json.RawMessage     `json:"params,omitempty"`
	Data                    json.RawMessage     `json:"data,omitempty"`
	Client                  json.RawMessage     `json:"client,omitempty"`
	Protocol                json.RawMessage     `json:"protocol,omitempty"`
	Error                   *WireError          `json:"error,omitempty"`
	Capabilities            []string            `json:"capabilities,omitempty"`
	LastBridgeEpoch         string              `json:"lastBridgeEpoch,omitempty"`
	LastEventID             string              `json:"lastEventId,omitempty"`
	LastSeenBySession       BridgeSessionCutMap `json:"lastSeenBySession,omitempty"`
	RecoveryID              string              `json:"recoveryId,omitempty"`
	AppliedThroughBySession BridgeSessionCutMap `json:"appliedThroughBySession,omitempty"`
}

type WireError struct {
	Code             string `json:"code,omitempty"`
	Message          string `json:"message,omitempty"`
	Retryable        *bool  `json:"retryable,omitempty"`
	RetryAfterMillis *int64 `json:"retryAfterMillis,omitempty"`
	Attempts         int    `json:"attempts,omitempty"`
}

func (e *WireError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// RegisterAck is the response to a register message.
type RegisterAck struct {
	Ok          bool            `json:"ok"`
	Protocol    *ProtocolResult `json:"protocol,omitempty"`
	Backends    []BackendInfo   `json:"backends,omitempty"`
	BridgeEpoch string          `json:"bridgeEpoch,omitempty"`
	Error       *WireError      `json:"error,omitempty"`
}

type ProtocolResult struct {
	Name           string `json:"name,omitempty"`
	Version        int    `json:"version,omitempty"`
	SchemaRevision string `json:"schemaRevision,omitempty"`
}

type BackendInfo struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	DisplayName  string            `json:"displayName,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Descriptor   map[string]string `json:"descriptor,omitempty"`
}

// Result response for RPC requests.
type ResultResponse struct {
	Ok   bool        `json:"ok"`
	Data interface{} `json:"data,omitempty"`
}

// ObservationSessionAttach is one session's Subscribe + observer attach outcome.
// Additive optional field on set_observation_scope (schemaRevision 2026-08-23).
type ObservationSessionAttach struct {
	SessionID  string `json:"sessionId"`
	Subscribed bool   `json:"subscribed"`
	Attached   bool   `json:"attached"`
	Error      string `json:"error,omitempty"`
}

// ObservationScopeRPCResult is the set_observation_scope data payload.
// Ok is true only when every requested session is subscribed and, if the
// backend implements ThreadLiveAttacher, observer-attached.
type ObservationScopeRPCResult struct {
	Ok       bool                       `json:"ok"`
	Sessions []ObservationSessionAttach `json:"sessions,omitempty"`
}

// EventMessage is pushed from server to client for agent events.
type EventMessage struct {
	Type          string      `json:"type"`
	EventID       string      `json:"eventId"`
	Seq           int         `json:"seq"`
	PerSessionSeq int         `json:"perSessionSeq,omitempty"`
	BridgeEpoch   string      `json:"bridgeEpoch"`
	SessionID     string      `json:"sessionId"`
	BackendID     string      `json:"backendId"`
	Event         string      `json:"event"`
	Data          interface{} `json:"data"`
	Message       string      `json:"message,omitempty"`
	Replayable    bool        `json:"replayable"`
	Timestamp     int64       `json:"timestamp"`
}

// Handler-specific request/response types.

type CreateSessionParams struct {
	Title       string `json:"title,omitempty"`
	Directory   string `json:"directory,omitempty"`
	AgentPreset string `json:"agentPreset,omitempty"`
}

type SendMessageParams struct {
	SessionID       string                 `json:"sessionId"`
	Content         string                 `json:"content"`
	Directory       string                 `json:"directory,omitempty"`
	Agent           string                 `json:"agent,omitempty"`
	Model           map[string]interface{} `json:"model,omitempty"`
	ReasoningEffort string                 `json:"reasoningEffort,omitempty"`
	Attachments     []AttachmentInput      `json:"attachments,omitempty"`
}

type AbortGenerationParams struct {
	SessionID string `json:"sessionId"`
	Directory string `json:"directory,omitempty"`
}

type ResumeSessionParams struct {
	SessionID string `json:"sessionId"`
	Directory string `json:"directory,omitempty"`
}

type GetSessionMessagesParams struct {
	SessionID           string `json:"sessionId"`
	Directory           string `json:"directory,omitempty"`
	Limit               int    `json:"limit,omitempty"`
	BeforeCursor        string `json:"beforeCursor,omitempty"`
	Paginate            bool   `json:"paginate,omitempty"`
	IfNoneMatchRevision string `json:"ifNoneMatchRevision,omitempty"`
	RecoveryID          string `json:"recoveryId,omitempty"`
}

type DeleteSessionParams struct {
	SessionID string `json:"sessionId"`
	Directory string `json:"directory,omitempty"`
}

type SetModelParams struct {
	SessionID string `json:"sessionId,omitempty"`
	Model     string `json:"model"`
	Directory string `json:"directory,omitempty"`
}

type SetPermissionModeParams struct {
	SessionID string `json:"sessionId,omitempty"`
	Mode      string `json:"mode"`
	Directory string `json:"directory,omitempty"`
}

type SetProviderParams struct {
	Provider  string `json:"provider"`
	Directory string `json:"directory,omitempty"`
}

type ResolvePermissionParams struct {
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Behavior  string `json:"behavior"` // "allow" or "deny"
}

type ListModelsParams struct {
	SessionID string `json:"sessionId,omitempty"`
	Directory string `json:"directory,omitempty"`
}

// SessionInfo is a legacy stub retained for historical reference. It is NOT the wire
// truth — list_sessions/get_session serialize through sessionsToWire (handlers.go), which
// builds an ad-hoc map from core.AgentSessionInfo, not this struct. The canonical field
// set is docs/protocol/schema/bridge-v1.types.ts BridgeSessionInfo. Do not add new wire
// fields here expecting them to reach iOS; edit sessionsToWire instead. Kept aligned with
// the verified wire union (incl. pinnedAtMillis) for documentation only.
type SessionInfo struct {
	ID               string `json:"id"`
	Title            string `json:"title,omitempty"`
	MessageCount     int    `json:"messageCount,omitempty"`
	ModifiedAt       string `json:"modifiedAt,omitempty"`
	PinnedAtMillis   int64  `json:"pinnedAtMillis,omitempty"`
	ArchivedAtMillis int64  `json:"archivedAtMillis,omitempty"`
	AgentPreset      string `json:"agentPreset,omitempty"`
}

// HistoryEntry returned to iOS.
type HistoryEntry struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

// Model info returned to iOS.
type ModelInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Provider   string `json:"provider,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	Reasoning  *bool  `json:"reasoning,omitempty"`
}

// ── Session registry ────────────────────────────────────────────────────────

type sessionState string

const (
	sessionStateIdle    sessionState = "idle"
	sessionStateRunning sessionState = "running"
	sessionStateClosing sessionState = "closing"
)

type trackedSession struct {
	session     core.AgentSession
	backendID   string
	sessionID   string
	directory   string
	state       sessionState
	lastUsedAt  time.Time
	lastEventAt time.Time
	pendingID   string // 非 空 时表示原始 pending ID，等待 rebind
}

type sessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]*trackedSession
	// onStateChange, if set, is invoked after a session transitions to running or
	// idle. It fires outside the registry mutex. Handlers uses it to invalidate
	// the cached Claude running map so the next list_sessions reflects owned-turn
	// transitions immediately instead of after the running-map TTL window.
	onStateChange func(backendID, sessionID, newState string)
}

type sessionActivityIdentity struct {
	backendID string
	sessionID string
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: make(map[string]*trackedSession)}
}

func (r *sessionRegistry) get(sessionID string) (*trackedSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.sessions[sessionID]
	return t, ok
}

func (r *sessionRegistry) getForBackend(sessionID, backendID string) (*trackedSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.sessions[sessionID]
	if !ok || t == nil || !sameBackendIdentity(t.backendID, backendID) {
		return nil, false
	}
	return t, true
}

func sameBackendIdentity(lhs, rhs string) bool {
	normalize := func(id string) string {
		if id == "claudecode" {
			return "claude"
		}
		return id
	}
	return lhs != "" && rhs != "" && normalize(lhs) == normalize(rhs)
}

func (r *sessionRegistry) put(sessionID, backendID, directory string, sess core.AgentSession) *trackedSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := &trackedSession{
		session:     sess,
		backendID:   backendID,
		sessionID:   sessionID,
		directory:   directory,
		state:       sessionStateIdle,
		lastUsedAt:  time.Now(),
		lastEventAt: time.Now(),
	}
	r.sessions[sessionID] = t
	return t
}

func (r *sessionRegistry) putRaw(sessionID string, sess core.AgentSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.sessions[sessionID]; ok {
		t.session = sess
	} else {
		r.sessions[sessionID] = &trackedSession{
			session:     sess,
			sessionID:   sessionID,
			state:       sessionStateIdle,
			lastUsedAt:  time.Now(),
			lastEventAt: time.Now(),
		}
	}
}

func (r *sessionRegistry) markRunning(sessionID string) {
	r.mu.Lock()
	var backendID string
	if t, ok := r.sessions[sessionID]; ok {
		t.state = sessionStateRunning
		t.lastUsedAt = time.Now()
		backendID = t.backendID
	} else {
		r.sessions[sessionID] = &trackedSession{
			sessionID:   sessionID,
			state:       sessionStateRunning,
			lastUsedAt:  time.Now(),
			lastEventAt: time.Now(),
		}
	}
	cb := r.onStateChange
	r.mu.Unlock()
	if cb != nil {
		cb(backendID, sessionID, string(sessionStateRunning))
	}
}

func (r *sessionRegistry) touch(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.sessions[sessionID]; ok {
		t.lastEventAt = time.Now()
	}
}

func (r *sessionRegistry) markIdle(sessionID string) {
	r.mu.Lock()
	var backendID string
	if t, ok := r.sessions[sessionID]; ok {
		t.state = sessionStateIdle
		t.lastEventAt = time.Now()
		backendID = t.backendID
	} else {
		r.sessions[sessionID] = &trackedSession{
			sessionID:   sessionID,
			state:       sessionStateIdle,
			lastUsedAt:  time.Now(),
			lastEventAt: time.Now(),
		}
	}
	cb := r.onStateChange
	r.mu.Unlock()
	if cb != nil {
		cb(backendID, sessionID, string(sessionStateIdle))
	}
}

func (r *sessionRegistry) isIdle(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.sessions[sessionID]
	return !ok || t.state == sessionStateIdle
}

// deleteIfSame CAS-deletes the registry entry for sessionID only when it
// still holds exactly sess (object identity). This mirrors the
// compare-and-delete pattern of clearRelayKindIf: racing replacements (abort,
// concurrent send, stale relay defer) can no longer evict a NEWER session,
// and exactly one CAS winner owns the out-of-lock Close/reap (design
// docs/2026-08-13-dsh-driver-design.md §3.6.3 CAS ownership). Returns the
// removed session when the CAS won.
func (r *sessionRegistry) deleteIfSame(sessionID string, sess core.AgentSession) (core.AgentSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.sessions[sessionID]
	if !ok || t == nil || t.session == nil || t.session != sess {
		return nil, false
	}
	if t.sessionID != "" {
		delete(r.sessions, t.sessionID)
	}
	if t.pendingID != "" {
		delete(r.sessions, t.pendingID)
	}
	delete(r.sessions, sessionID)
	return t.session, true
}

func (r *sessionRegistry) delete(sessionID string) (core.AgentSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.sessions[sessionID]
	if !ok {
		return nil, false
	}
	if t.sessionID != "" {
		delete(r.sessions, t.sessionID)
	}
	if t.pendingID != "" {
		delete(r.sessions, t.pendingID)
	}
	delete(r.sessions, sessionID) // 兜底删除
	return t.session, true
}

func (r *sessionRegistry) rebind(oldID, newID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.sessions[oldID]
	if !ok {
		return
	}
	t.sessionID = newID
	t.pendingID = oldID
	r.sessions[newID] = t
	// 保留 pending ID 的映射，resolveSessionIDForActiveSession 依赖它
}

func (r *sessionRegistry) forEach(fn func(sessionID string, t *trackedSession)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, t := range r.sessions {
		fn(id, t)
	}
}

// activitySnapshot 返回 Bridge-owned 活跃 turn 数。rebind 期间同一 trackedSession
// 可能同时以 pending/new id 出现在 map，必须按对象身份去重。
func (r *sessionRegistry) activitySnapshot() uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[*trackedSession]struct{}, len(r.sessions))
	var active uint32
	for _, t := range r.sessions {
		if t == nil {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		if t.state == sessionStateRunning {
			active++
		}
	}
	return active
}

func (r *sessionRegistry) activityIdentities() []sessionActivityIdentity {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[*trackedSession]struct{}, len(r.sessions))
	result := make([]sessionActivityIdentity, 0, len(r.sessions))
	for _, t := range r.sessions {
		if t == nil {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		if t.backendID != "" && t.sessionID != "" {
			result = append(result, sessionActivityIdentity{backendID: t.backendID, sessionID: t.sessionID})
		}
	}
	return result
}

// drain empties the registry, returning the sessions that were present. Used by
// Handlers.Shutdown to snapshot-and-clear under the lock before closing each
// session outside the lock.
func (r *sessionRegistry) drain() []core.AgentSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	var sessions []core.AgentSession
	for _, t := range r.sessions {
		if t.session != nil {
			sessions = append(sessions, t.session)
		}
	}
	for k := range r.sessions {
		delete(r.sessions, k)
	}
	return sessions
}

func (r *sessionRegistry) directoryForSession(sessionID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.sessions[sessionID]; ok {
		return t.directory
	}
	return ""
}

// idleTTL returns the idle timeout for a given backend type.
func idleTTL(backendID string) time.Duration {
	switch backendID {
	case "codex":
		return 600 * time.Second
	default:
		return 300 * time.Second
	}
}

// ── Broadcaster ──────────────────────────────────────────────────────────────

type SubscriptionKey struct {
	BackendID string
	SessionID string
	Directory string
}

type BroadcastEvent struct {
	BackendID string
	SessionID string
	Directory string
	Message   interface{}
}

type Broadcaster struct {
	mu          sync.Mutex
	allConns    map[Connection]struct{}                     // 已认证/已建立的 bridge 连接，用于被动事件无订阅时兜住全局广播
	subscribers map[SubscriptionKey]map[Connection]struct{} // key -> set of conns
	connSubs    map[Connection]map[SubscriptionKey]struct{} // conn -> set of keys
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		allConns:    make(map[Connection]struct{}),
		subscribers: make(map[SubscriptionKey]map[Connection]struct{}),
		connSubs:    make(map[Connection]map[SubscriptionKey]struct{}),
	}
}

func (b *Broadcaster) RegisterConn(conn Connection) {
	if conn == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allConns[conn] = struct{}{}
}

func (b *Broadcaster) HasConnections() bool {
	b.mu.Lock()
	conns := make([]Connection, 0, len(b.allConns))
	for conn := range b.allConns {
		conns = append(conns, conn)
	}
	b.mu.Unlock()
	for _, conn := range conns {
		if !connectionIsClosed(conn) {
			return true
		}
	}
	return false
}

func (b *Broadcaster) Subscribe(conn Connection, key SubscriptionKey) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allConns[conn] = struct{}{}
	if b.subscribers[key] == nil {
		b.subscribers[key] = make(map[Connection]struct{})
	}
	b.subscribers[key][conn] = struct{}{}
	if b.connSubs[conn] == nil {
		b.connSubs[conn] = make(map[SubscriptionKey]struct{})
	}
	b.connSubs[conn][key] = struct{}{}
}

func (b *Broadcaster) UnsubscribeAll(conn Connection) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key := range b.connSubs[conn] {
		delete(b.subscribers[key], conn)
		if len(b.subscribers[key]) == 0 {
			delete(b.subscribers, key)
		}
	}
	delete(b.connSubs, conn)
	delete(b.allConns, conn)
}

// TransferSubscriptions moves every session subscription from old → new under one lock.
// Used when a Relay device re-handshakes: the new Connection must inherit the old
// session interest immediately so live text/reasoning deltas keep a target.
// Observation scope is device-scoped (not connection-scoped) and is left alone.
func (b *Broadcaster) TransferSubscriptions(old, new Connection) {
	if old == nil || new == nil || old == new {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := b.connSubs[old]
	if len(keys) == 0 {
		delete(b.allConns, old)
		b.allConns[new] = struct{}{}
		return
	}
	if b.connSubs[new] == nil {
		b.connSubs[new] = make(map[SubscriptionKey]struct{})
	}
	for key := range keys {
		if b.subscribers[key] == nil {
			b.subscribers[key] = make(map[Connection]struct{})
		}
		delete(b.subscribers[key], old)
		b.subscribers[key][new] = struct{}{}
		b.connSubs[new][key] = struct{}{}
	}
	delete(b.connSubs, old)
	delete(b.allConns, old)
	b.allConns[new] = struct{}{}
}

func (b *Broadcaster) ActiveDeviceIDs() []string {
	b.mu.Lock()
	conns := make([]Connection, 0, len(b.allConns))
	for conn := range b.allConns {
		conns = append(conns, conn)
	}
	b.mu.Unlock()
	seen := make(map[string]struct{})
	for _, conn := range conns {
		if connectionIsClosed(conn) {
			continue
		}
		if device := conn.AuthedDevice(); device != nil {
			seen[device.DeviceID] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	return result
}

func (b *Broadcaster) Rebind(oldID, newID, backendID, directory string) {
	// Rebind ALL keys that match backend+oldSession regardless of Directory.
	// set_observation_scope Subscribes with Directory="", while rebindSessionIDIfResolved
	// often passes the workdir — a single-key rebind was a no-op and left a ghost
	// pending-* subscription (codex file relay thrash + zero live targets on real id).
	_ = directory
	b.mu.Lock()
	defer b.mu.Unlock()

	type move struct {
		oldKey SubscriptionKey
		newKey SubscriptionKey
		conns  map[Connection]struct{}
	}
	var moves []move
	for key, conns := range b.subscribers {
		if key.BackendID != backendID || key.SessionID != oldID || len(conns) == 0 {
			continue
		}
		// Copy conn set; key is a value type so safe to capture.
		copied := make(map[Connection]struct{}, len(conns))
		for c := range conns {
			copied[c] = struct{}{}
		}
		moves = append(moves, move{
			oldKey: key,
			newKey: SubscriptionKey{BackendID: backendID, SessionID: newID, Directory: key.Directory},
			conns:  copied,
		})
	}
	for _, m := range moves {
		// Merge into existing newKey subscribers if any.
		if b.subscribers[m.newKey] == nil {
			b.subscribers[m.newKey] = make(map[Connection]struct{})
		}
		for conn := range m.conns {
			b.subscribers[m.newKey][conn] = struct{}{}
			if b.connSubs[conn] == nil {
				b.connSubs[conn] = make(map[SubscriptionKey]struct{})
			}
			b.connSubs[conn][m.newKey] = struct{}{}
			delete(b.connSubs[conn], m.oldKey)
		}
		delete(b.subscribers, m.oldKey)
	}
}

func (b *Broadcaster) Send(ev BroadcastEvent) {
	for _, conn := range b.Targets(ev.BackendID, ev.SessionID, ev.Directory) {
		conn.SendJSON(ev.Message)
	}
}

func (b *Broadcaster) Targets(backendID, sessionID, directory string) []Connection {
	b.mu.Lock()
	targets := make(map[Connection]struct{})
	// 精确匹配
	key := SubscriptionKey{BackendID: backendID, SessionID: sessionID, Directory: directory}
	for conn := range b.subscribers[key] {
		targets[conn] = struct{}{}
	}
	// 不带 directory 匹配：event 有 directory 时也尝试匹配无 directory 的订阅者
	if directory != "" {
		noDirKey := SubscriptionKey{BackendID: backendID, SessionID: sessionID}
		for conn := range b.subscribers[noDirKey] {
			targets[conn] = struct{}{}
		}
	}
	// event 无 directory 时，匹配该 session 所有 directory 的订阅者
	if directory == "" {
		prefix := SubscriptionKey{BackendID: backendID, SessionID: sessionID}
		for k, conns := range b.subscribers {
			if k.BackendID == prefix.BackendID && k.SessionID == prefix.SessionID && k.Directory != "" {
				for conn := range conns {
					targets[conn] = struct{}{}
				}
			}
		}
	}
	// Fallback: 如果以上匹配都没有找到订阅者，广播给该 backend 的所有连接。
	// 这确保被动订阅者（Codex Passive Subscriber / OpenCode SSE）的事件
	// 在 iOS 尚未通过 get_session_messages 订阅具体 session 时也能送达。
	// 与老路径 register 模式的无条件广播行为一致。
	if len(targets) == 0 {
		for k, conns := range b.subscribers {
			if k.BackendID == backendID {
				for conn := range conns {
					targets[conn] = struct{}{}
				}
			}
		}
	}
	// 如果连接已经建立但还没订阅任何 session（例如 App 刚启动停在 session 列表，
	// Mac 端立刻发起 OpenCode 任务），也要把被动事件送到 iOS。事件信封包含
	// backendID/sessionID，客户端会继续按当前 backend/session 做过滤或刷新。
	if len(targets) == 0 {
		for conn := range b.allConns {
			targets[conn] = struct{}{}
		}
	}
	// Fix 5: 每 token 一帧的 INFO 日志降级为 Debug（长答时数十~上百帧/秒的 I/O 开销）。
	// 不影响诊断——广播路径仍可由 Debug 级别观察；relayEvents 关键里程碑（turn_*/error）另 Info 记录。
	slog.Debug("go-bridge: broadcast targets", "backend", backendID, "session", sessionID, "dir", directory, "targets", len(targets), "fallback", len(targets) > 0 && len(targets) == len(b.connSubs))
	b.mu.Unlock()
	result := make([]Connection, 0, len(targets))
	for conn := range targets {
		// Never deliver live frames to a replaced/closed connection — they would
		// only produce "drop outbound on closed" and starve the new generation.
		if connectionIsClosed(conn) {
			continue
		}
		result = append(result, conn)
	}
	return result
}

// ── Pending Notification Store ──────────────────────────────────────────────
// 记录 turn 完成但 iOS 设备可能未收到的事件（因 iOS 被系统挂起）。
// iOS 回前台时通过 check_pending_notifications RPC 拉取并弹出本地通知。

type PendingNotification struct {
	SessionID   string    `json:"sessionId"`
	BackendID   string    `json:"backendId"`
	Directory   string    `json:"directory,omitempty"`
	Title       string    `json:"title,omitempty"`
	Reason      string    `json:"reason"` // "completed" | "error"
	Message     string    `json:"message,omitempty"`
	CompletedAt time.Time `json:"completedAt"`
}

type PendingNotificationStore struct {
	mu    sync.Mutex
	items map[string][]PendingNotification // deviceID -> pending list
}

func NewPendingNotificationStore() *PendingNotificationStore {
	return &PendingNotificationStore{
		items: make(map[string][]PendingNotification),
	}
}

func (s *PendingNotificationStore) Record(deviceID string, n PendingNotification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[deviceID] = append(s.items[deviceID], n)
	// 限制每个设备最多 50 条，防止内存泄漏
	if len(s.items[deviceID]) > 50 {
		s.items[deviceID] = s.items[deviceID][len(s.items[deviceID])-50:]
	}
}

func (s *PendingNotificationStore) Consume(deviceID string) []PendingNotification {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.items[deviceID]
	delete(s.items, deviceID)
	return result
}

// SubscriberDeviceIDs 返回订阅了指定 session 的所有（已认证）设备 ID。
func (b *Broadcaster) SubscriberDeviceIDs(backendID, sessionID string) []string {
	b.mu.Lock()
	conns := make([]Connection, 0)
	for k, subscribers := range b.subscribers {
		if k.BackendID != backendID || k.SessionID != sessionID {
			continue
		}
		for conn := range subscribers {
			conns = append(conns, conn)
		}
	}
	b.mu.Unlock()
	seen := make(map[string]struct{})
	for _, conn := range conns {
		if connectionIsClosed(conn) {
			continue
		}
		if device := conn.AuthedDevice(); device != nil {
			seen[device.DeviceID] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	return result
}

// HasSessionSubscriber reports whether ANY connection is currently subscribed to
// the given session (any directory; authed or not). Per-session relays use this to
// keep watching (push model) while a client has the session open, instead of exiting
// on idle TTL and missing the external turn.
func (b *Broadcaster) HasSessionSubscriber(backendID, sessionID string) bool {
	b.mu.Lock()
	conns := make([]Connection, 0)
	for k, subscribers := range b.subscribers {
		if k.BackendID != backendID || k.SessionID != sessionID {
			continue
		}
		for conn := range subscribers {
			conns = append(conns, conn)
		}
	}
	b.mu.Unlock()
	for _, conn := range conns {
		if !connectionIsClosed(conn) {
			return true
		}
	}
	return false
}

// SubscribedSessionIDs returns the set of session IDs that currently have at least one
// subscriber for the given backend (a client has that session open). Used by the codex
// relay safety-net watcher to keep a relay running for every open session.
func (b *Broadcaster) SubscribedSessionIDs(backendID string) []string {
	b.mu.Lock()
	subs := make(map[string][]Connection)
	for k, conns := range b.subscribers {
		if k.BackendID != backendID {
			continue
		}
		for conn := range conns {
			subs[k.SessionID] = append(subs[k.SessionID], conn)
		}
	}
	b.mu.Unlock()
	seen := make(map[string]struct{}, len(subs))
	for sessionID, conns := range subs {
		for _, conn := range conns {
			if !connectionIsClosed(conn) {
				seen[sessionID] = struct{}{}
				break
			}
		}
	}
	out := make([]string, 0, len(seen))
	for sid := range seen {
		out = append(out, sid)
	}
	return out
}

// connectionIsClosed is the optional liveness seam shared by direct and relay
// connections. A connection implementation that does not expose it is treated
// as open for backwards compatibility with test and extension adapters.
func connectionIsClosed(conn Connection) bool {
	if conn == nil {
		return true
	}
	closed, ok := conn.(interface{ isClosed() bool })
	return ok && closed.isClosed()
}
