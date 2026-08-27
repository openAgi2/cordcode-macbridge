package gobridge

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// web_push_candidate.go — PushIntent → candidate pipeline（web push 方案 §8.1/§8.3）。
//
// 生产者（agent relay loop / startPassiveSubscription，见 D3 位点清单）在 LogicalEvent 上
// 携带 PushIntent；EventPublisher 在 authoritative Kernel ingest 之后、transport 分支之前
// 把 (EventMessage, PushIntent) 交给本 pipeline。锁约束（§8.1 candidate sink 锁与背压）：
// Ingest 在 publisher ordering lock 内执行，只做小对象复制、内存 map 读写和非阻塞
// 有界 channel 写入——不做磁盘、网络、大 JSON 序列化或等待 channel。ledger 持久化、
// subscription fan-out 与发送全部在锁外 worker（E1 dispatcher）执行。

// webPushCandidateQueueCapacity 是 dispatcher 前的有界队列容量。满时显式 queue_full
// 计数 + 脱敏诊断，事件投递继续，本次推送 fail closed。
const webPushCandidateQueueCapacity = 256

// PushIntent 是 LogicalEvent 的非 wire 内部字段：producer 声明"此事件值得通知"。
// 默认 nil = 不发送。hydrate source、recovery replay、derived question_asked、catalog、
// control-plane 永不设置（producer 位点清单，§8.1）。
type PushIntent struct {
	Kind            WebPushNotificationKind
	NotificationKey string
	AnchorKind      string
	AnchorID        string
	// SessionTitle 是清洗/截断后的 authoritative catalog 标题（设计 delta §2.2）；
	// 空串 = 缓存未命中，通知按无标题回退。随 candidate 小拷贝流转，不含正文。
	SessionTitle string
}

// WebPushCandidate 是交给 dispatcher 的不可变小对象（不含正文/密钥材料）。
type WebPushCandidate struct {
	BridgeID        string
	BackendID       string
	SessionID       string
	EventID         string
	Kind            WebPushNotificationKind
	NotificationKey string
	AnchorKind      string
	AnchorID        string
	SessionTitle    string
	ReceivedAt      int64
}

// WebPushCandidateSink 是 EventPublisher 依赖的注入点（D1 三态驱动）。
type WebPushCandidateSink interface {
	// Ingest 在 publisher ordering lock 内被调用：applied → 有界入队；
	// deferred → 按 eventId 登记待 hydrate commit 释放；no_change → 丢弃。
	Ingest(candidate WebPushCandidate, result ProjectionIngestResult)
	// ReleaseDeferred 在 Kernel/EventPublisher 锁外调用：把 commit 接受的
	// deferred candidate 送入队列。
	ReleaseDeferred(eventIDs []string)
	// DiscardDeferred 在 hydrate MarkFailed 后调用：丢弃未提交的 deferred
	// candidate 并记录脱敏诊断，不得伪装成功通知。
	DiscardDeferred(eventIDs []string)
}

// WebPushCandidatePipeline 是 WebPushCandidateSink 的实现：有界队列 + deferred 注册表
// + ledger 去重。store 为 nil 时（dev 模式/未接线）一切 fail closed：队列保持空。
type WebPushCandidatePipeline struct {
	store    *WebPushStore
	bridgeID string

	queue    chan WebPushCandidate
	mu       sync.Mutex
	deferred map[string]WebPushCandidate // eventID → candidate（hydrate 窗口）

	enqueued     atomic.Int64
	droppedNoop  atomic.Int64
	queueFull    atomic.Int64
	ledgerDedup  atomic.Int64
	released     atomic.Int64
	discarded    atomic.Int64
	invalidEarly atomic.Int64
}

// SetBridgeID 记录本 bridge 身份（main.go 启动时注入一次；candidate 深链 target 用）。
func (p *WebPushCandidatePipeline) SetBridgeID(bridgeID string) {
	p.bridgeID = bridgeID
}

func NewWebPushCandidatePipeline(store *WebPushStore) *WebPushCandidatePipeline {
	return &WebPushCandidatePipeline{
		store:    store,
		queue:    make(chan WebPushCandidate, webPushCandidateQueueCapacity),
		deferred: make(map[string]WebPushCandidate),
	}
}

// Ingest implements WebPushCandidateSink（publisher lock 内；只做内存操作）。
func (p *WebPushCandidatePipeline) Ingest(candidate WebPushCandidate, result ProjectionIngestResult) {
	if p == nil {
		return
	}
	switch result {
	case ProjectionIngestApplied:
		p.enqueueLocked(candidate)
	case ProjectionIngestDeferred:
		if candidate.EventID == "" {
			// identity 不完整的 deferred candidate 无法在 commit 后对账：fail closed。
			p.invalidEarly.Add(1)
			return
		}
		p.mu.Lock()
		p.deferred[candidate.EventID] = candidate
		p.mu.Unlock()
	case ProjectionIngestNoChange:
		p.droppedNoop.Add(1)
	default:
		p.droppedNoop.Add(1)
	}
}

// enqueueLocked 名字沿用"非阻塞"语义：绝不阻塞；queue 满时 fail closed。
// ledger 去重走 store 内存 map（ mutex 保护的小读，不做磁盘 IO）。
func (p *WebPushCandidatePipeline) enqueueLocked(candidate WebPushCandidate) {
	if p.store == nil {
		return // store 未接线：不产生任何 candidate（不冒充已发送）
	}
	candidate.BridgeID = p.bridgeID
	if p.store.LedgerShouldSend(WebPushNotificationKeyHash(candidate.NotificationKey)) {
		select {
		case p.queue <- candidate:
			p.enqueued.Add(1)
		default:
			p.queueFull.Add(1)
			slog.Warn("web-push: candidate queue full (fail closed for this notification)",
				"backendID", candidate.BackendID,
				"sessionPrefix", projectionSessionLogPrefix(candidate.SessionID),
				"kind", string(candidate.Kind),
			)
		}
		return
	}
	p.ledgerDedup.Add(1)
}

// ReleaseDeferred implements WebPushCandidateSink（hydrate commit 成功后、锁外调用）。
func (p *WebPushCandidatePipeline) ReleaseDeferred(eventIDs []string) {
	if p == nil || len(eventIDs) == 0 {
		return
	}
	p.mu.Lock()
	pending := make([]WebPushCandidate, 0, len(eventIDs))
	for _, id := range eventIDs {
		if candidate, ok := p.deferred[id]; ok {
			delete(p.deferred, id)
			pending = append(pending, candidate)
		}
	}
	p.mu.Unlock()
	for _, candidate := range pending {
		p.enqueueLocked(candidate)
		p.released.Add(1)
	}
}

// DiscardDeferred implements WebPushCandidateSink（hydrate 失败后调用；显式诊断、不假发送）。
func (p *WebPushCandidatePipeline) DiscardDeferred(eventIDs []string) {
	if p == nil || len(eventIDs) == 0 {
		return
	}
	dropped := 0
	p.mu.Lock()
	for _, id := range eventIDs {
		if candidate, ok := p.deferred[id]; ok {
			delete(p.deferred, id)
			dropped++
			slog.Warn("web-push: deferred_hydrate_failed (candidate discarded, no send)",
				"backendID", candidate.BackendID,
				"sessionPrefix", projectionSessionLogPrefix(candidate.SessionID),
				"kind", string(candidate.Kind),
			)
		}
	}
	p.mu.Unlock()
	p.discarded.Add(int64(dropped))
}

// C 返回候选队列的接收端（dispatcher worker select 用）。只读；入队仍走 Ingest/Release。
func (p *WebPushCandidatePipeline) C() <-chan WebPushCandidate {
	if p == nil {
		ch := make(chan WebPushCandidate)
		return ch
	}
	return p.queue
}

// Drain 取走当前排队 candidate（dispatcher worker / 测试用；非阻塞快照式取空）。
func (p *WebPushCandidatePipeline) Drain() []WebPushCandidate {
	if p == nil {
		return nil
	}
	out := make([]WebPushCandidate, 0, len(p.queue))
	for {
		select {
		case c := <-p.queue:
			out = append(out, c)
		default:
			return out
		}
	}
}

// Stats 返回计数快照（诊断/测试）。
func (p *WebPushCandidatePipeline) Stats() (enqueued, droppedNoop, queueFull, ledgerDedup, released, discarded, invalidEarly int64) {
	return p.enqueued.Load(), p.droppedNoop.Load(), p.queueFull.Load(), p.ledgerDedup.Load(), p.released.Load(), p.discarded.Load(), p.invalidEarly.Load()
}

// DeferredCount 返回当前 deferred 注册表大小（测试用）。
func (p *WebPushCandidatePipeline) DeferredCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.deferred)
}
