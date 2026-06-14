package gobridge

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/openAgi2/cccode-macbridge/core"
)

// WireMessage is the top-level envelope for all WS messages.
type WireMessage struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId,omitempty"`
	BackendID string          `json:"backendId,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Operation string          `json:"operation,omitempty"`
	Event     string          `json:"event,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Client    json.RawMessage `json:"client,omitempty"`
	Protocol  json.RawMessage `json:"protocol,omitempty"`
	Error     *WireError      `json:"error,omitempty"`
}

type WireError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
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

// EventMessage is pushed from server to client for agent events.
type EventMessage struct {
	Type      string      `json:"type"`
	SessionID string      `json:"sessionId"`
	BackendID string      `json:"backendId"`
	Event     string      `json:"event"`
	Data      interface{} `json:"data"`
	Seq       int         `json:"seq,omitempty"`
}

// Handler-specific request/response types.

type CreateSessionParams struct {
	Title     string `json:"title,omitempty"`
	Directory string `json:"directory,omitempty"`
}

type SendMessageParams struct {
	SessionID       string                 `json:"sessionId"`
	Content         string                 `json:"content"`
	Directory       string                 `json:"directory,omitempty"`
	Model           map[string]interface{} `json:"model,omitempty"`
	ReasoningEffort string                 `json:"reasoningEffort,omitempty"`
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
	SessionID    string `json:"sessionId"`
	Directory    string `json:"directory,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	BeforeCursor string `json:"beforeCursor,omitempty"`
	Paginate     bool   `json:"paginate,omitempty"`
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

// Session info returned to iOS.
type SessionInfo struct {
	ID           string `json:"id"`
	Title        string `json:"title,omitempty"`
	MessageCount int    `json:"messageCount,omitempty"`
	ModifiedAt   string `json:"modifiedAt,omitempty"`
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
	defer r.mu.Unlock()
	if t, ok := r.sessions[sessionID]; ok {
		t.state = sessionStateRunning
		t.lastUsedAt = time.Now()
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
	defer r.mu.Unlock()
	if t, ok := r.sessions[sessionID]; ok {
		t.state = sessionStateIdle
		t.lastEventAt = time.Now()
	}
}

func (r *sessionRegistry) isIdle(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.sessions[sessionID]
	return !ok || t.state == sessionStateIdle
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
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allConns[conn] = struct{}{}
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

func (b *Broadcaster) ActiveDeviceIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := make(map[string]struct{})
	for conn := range b.allConns {
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
	oldKey := SubscriptionKey{BackendID: backendID, SessionID: oldID, Directory: directory}
	newKey := SubscriptionKey{BackendID: backendID, SessionID: newID, Directory: directory}
	b.mu.Lock()
	defer b.mu.Unlock()
	conns, ok := b.subscribers[oldKey]
	if !ok {
		return
	}
	b.subscribers[newKey] = conns
	delete(b.subscribers, oldKey)
	for conn := range conns {
		b.connSubs[conn][newKey] = struct{}{}
		delete(b.connSubs[conn], oldKey)
	}
}

func (b *Broadcaster) Send(ev BroadcastEvent) {
	b.mu.Lock()
	targets := make(map[Connection]struct{})
	// 精确匹配
	key := SubscriptionKey{BackendID: ev.BackendID, SessionID: ev.SessionID, Directory: ev.Directory}
	for conn := range b.subscribers[key] {
		targets[conn] = struct{}{}
	}
	// 不带 directory 匹配：event 有 directory 时也尝试匹配无 directory 的订阅者
	if ev.Directory != "" {
		noDirKey := SubscriptionKey{BackendID: ev.BackendID, SessionID: ev.SessionID}
		for conn := range b.subscribers[noDirKey] {
			targets[conn] = struct{}{}
		}
	}
	// event 无 directory 时，匹配该 session 所有 directory 的订阅者
	if ev.Directory == "" {
		prefix := SubscriptionKey{BackendID: ev.BackendID, SessionID: ev.SessionID}
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
			if k.BackendID == ev.BackendID {
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
	slog.Info("go-bridge: broadcast", "backend", ev.BackendID, "session", ev.SessionID, "dir", ev.Directory, "targets", len(targets), "fallback", len(targets) > 0 && len(targets) == len(b.connSubs))
	b.mu.Unlock()

	for conn := range targets {
		conn.SendJSON(ev.Message)
	}
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

// SubscriberDeviceIDs 返回订阅了指定 session 的所有设备 ID。
func (b *Broadcaster) SubscriberDeviceIDs(backendID, sessionID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := make(map[string]struct{})
	// 遍历所有 key，匹配 backendID + sessionID
	for k, conns := range b.subscribers {
		if k.BackendID != backendID || k.SessionID != sessionID {
			continue
		}
		for conn := range conns {
			if device := conn.AuthedDevice(); device != nil {
				seen[device.DeviceID] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	return result
}
