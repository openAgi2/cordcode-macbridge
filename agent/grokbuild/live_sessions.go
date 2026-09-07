package grokbuild

// live_sessions.go — Agent 侧活会话 actor 注册表（方案 §4.2/§8 p1b：
// sessionId 贯通）。
//
// bridge 持有 core.AgentSession 注册表（go-bridge sessionRegistry），但
// ExecuteSessionCommand 是 Agent 级接口（core.SessionCommandCatalog），拿不到
// bridge 的表；Grok 命令是 prompt 语义 host 动作，必须在**会话自己的活 actor**
// 上经共用 turn dispatcher 执行（官方反馈正文经该 actor 的 session/update 流
// 出，进同一 Events 轨），绝不能另起 child 或降级普通消息。因此 grokSession
// 握手成功后向 Agent 登记、Close/进程退出时按指针身份 CAS 撤销。
//
// 不登记的 actor：List 专用 pull child（acuObs != nil）——它是短命目录拉取
// 进程，不是会话对话轨。

import "sync"

// liveSessions is the pointer-identity registry of conversation actors.
// Mirror of the bridge's deleteSessionIfSame CAS discipline: a respawned
// actor for the same sessionID replaces the entry; a dying stale actor's
// unregister can never evict the replacement.
type liveSessions struct {
	mu   sync.Mutex
	byID map[string]*grokSession
}

func newLiveSessions() *liveSessions {
	return &liveSessions{byID: make(map[string]*grokSession)}
}

// register records sess under sessionID unless a live actor already holds it.
func (l *liveSessions) register(sessionID string, sess *grokSession) bool {
	if sessionID == "" || sess == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if cur, ok := l.byID[sessionID]; ok && cur != sess && cur.Alive() {
		return false
	}
	l.byID[sessionID] = sess
	return true
}

// unregister drops the entry only when it still maps to exactly sess.
func (l *liveSessions) unregister(sessionID string, sess *grokSession) {
	if sessionID == "" || sess == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byID[sessionID] == sess {
		delete(l.byID, sessionID)
	}
}

// get returns the live actor for sessionID (nil when absent or dead).
func (l *liveSessions) get(sessionID string) (*grokSession, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.byID[sessionID]
	if !ok || !s.Alive() {
		return nil, false
	}
	return s, true
}

// --- Agent-level accessors (nil-safe: tests construct Agent literally) ---

func (a *Agent) liveRegistry() *liveSessions {
	if a.live != nil {
		return a.live
	}
	a.liveInitMu.Lock()
	defer a.liveInitMu.Unlock()
	if a.live == nil {
		a.live = newLiveSessions()
	}
	return a.live
}

func (a *Agent) registerLiveSession(sessionID string, s *grokSession) {
	a.liveRegistry().register(sessionID, s)
}

func (a *Agent) unregisterLiveSession(sessionID string, s *grokSession) {
	if a.live == nil {
		return
	}
	a.live.unregister(sessionID, s)
}

// liveSessionForCommand resolves the conversation actor a slash line must be
// dispatched on (§4.2: 共用 turn dispatcher — the session's own rail, never a
// fresh child, never plain-message degradation).
func (a *Agent) liveSessionForCommand(sessionID string) (*grokSession, bool) {
	return a.liveRegistry().get(sessionID)
}
