package gobridge

import (
	"testing"
	"time"
)

// D2 — PushIntent → candidate pipeline（web push §8.1/§8.3）。
//
// 覆盖 Gate D 相关管线语义：三态入队、ledger 按 NotificationKey 去重（同 key 只入队
// 一次）、hydrate deferred 注册→commit 释放（commit 前不入队、commit 后恰好一次）、
// MarkFailed 丢弃、queue_full fail closed、store 未接线不产生 candidate、
// EventPublisher 位点（PushIntent 出现在非 kernel 路径不产生 candidate）。

func newCandidatePipelineForTest(t *testing.T) (*WebPushCandidatePipeline, *WebPushStore) {
	t.Helper()
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_pipe_test")
	return pipeline, store
}

func completionCandidate(eventID string) WebPushCandidate {
	return WebPushCandidate{
		BackendID:       "codex",
		SessionID:       "cand-1",
		EventID:         eventID,
		Kind:            WebPushKindCompletion,
		NotificationKey: "codex|cand-1|turn-1|completed",
		AnchorKind:      "turn",
		AnchorID:        "turn-1",
		ReceivedAt:      42,
	}
}

func TestCandidatePipelineAppliedEnqueues(t *testing.T) {
	p, _ := newCandidatePipelineForTest(t)
	p.Ingest(completionCandidate("e1:1"), ProjectionIngestApplied)
	got := p.Drain()
	if len(got) != 1 {
		t.Fatalf("queued = %d, want 1", len(got))
	}
	if got[0].BridgeID != "brg_pipe_test" {
		t.Fatalf("BridgeID = %q (stamped by pipeline)", got[0].BridgeID)
	}
	if got[0].EventID != "e1:1" || got[0].Kind != WebPushKindCompletion {
		t.Fatalf("candidate = %+v", got[0])
	}
}

func TestCandidatePipelineNoChangeDrops(t *testing.T) {
	p, _ := newCandidatePipelineForTest(t)
	p.Ingest(completionCandidate("e1:1"), ProjectionIngestNoChange)
	if got := p.Drain(); len(got) != 0 {
		t.Fatalf("no_change must drop, queued %d", len(got))
	}
}

func TestCandidatePipelineLedgerDedupSameKeyOnce(t *testing.T) {
	p, store := newCandidatePipelineForTest(t)
	key := "codex|cand-1|turn-1|completed"
	p.Ingest(completionCandidate("e1:1"), ProjectionIngestApplied)
	// 模拟 dispatcher 已成功送达（accepted 入账）。
	store.LedgerRecord(WebPushNotificationKeyHash(key), "e1:1", "wps_x", "accepted")
	p.Ingest(completionCandidate("e1:2"), ProjectionIngestApplied) // 同 key 不同 eventId
	got := p.Drain()
	if len(got) != 1 || got[0].EventID != "e1:1" {
		t.Fatalf("ledger must suppress same-key re-enqueue: %+v", got)
	}
	// temporary_failed 不抑制重试。
	store.LedgerRecord(WebPushNotificationKeyHash(key), "e1:2", "wps_x", "temporary_failed")
	p.Ingest(completionCandidate("e1:3"), ProjectionIngestApplied)
	if got := p.Drain(); len(got) != 1 || got[0].EventID != "e1:3" {
		t.Fatalf("temporary_failed must allow retry: %+v", got)
	}
}

func TestCandidatePipelineDeferredReleaseOnCommit(t *testing.T) {
	p, _ := newCandidatePipelineForTest(t)
	p.Ingest(completionCandidate("e1:9"), ProjectionIngestDeferred)
	if got := p.Drain(); len(got) != 0 {
		t.Fatalf("deferred must NOT enqueue before commit, got %+v", got)
	}
	if p.DeferredCount() != 1 {
		t.Fatalf("deferred registry = %d", p.DeferredCount())
	}
	// commit 只接受部分 pending：未接受的 eventId 不释放。
	p.ReleaseDeferred([]string{"e1:other"})
	if got := p.Drain(); len(got) != 0 {
		t.Fatalf("unaccepted id must not release: %+v", got)
	}
	p.ReleaseDeferred([]string{"e1:9"})
	got := p.Drain()
	if len(got) != 1 || got[0].EventID != "e1:9" {
		t.Fatalf("accepted deferred must release exactly once: %+v", got)
	}
	// 释放是消耗性的：再次 Release 不重复入队。
	p.ReleaseDeferred([]string{"e1:9"})
	if got := p.Drain(); len(got) != 0 {
		t.Fatalf("double release must be a no-op: %+v", got)
	}
}

func TestCandidatePipelineDeferredWithoutEventIDDrops(t *testing.T) {
	p, _ := newCandidatePipelineForTest(t)
	c := completionCandidate("")
	p.Ingest(c, ProjectionIngestDeferred)
	if p.DeferredCount() != 0 {
		t.Fatal("identity-less deferred candidate must be dropped (fail closed)")
	}
}

func TestCandidatePipelineDiscardOnHydrateFailure(t *testing.T) {
	p, _ := newCandidatePipelineForTest(t)
	p.Ingest(completionCandidate("e1:5"), ProjectionIngestDeferred)
	p.Ingest(completionCandidate("e1:6"), ProjectionIngestDeferred)
	p.DiscardDeferred([]string{"e1:5", "e1:6"})
	if got := p.Drain(); len(got) != 0 {
		t.Fatalf("discarded deferred must never enqueue: %+v", got)
	}
	if p.DeferredCount() != 0 {
		t.Fatal("registry not cleared")
	}
	// 丢弃后再 release 同 id：不得复活。
	p.ReleaseDeferred([]string{"e1:5"})
	if got := p.Drain(); len(got) != 0 {
		t.Fatalf("discarded candidate resurrected: %+v", got)
	}
}

func TestCandidatePipelineNilStoreFailsClosed(t *testing.T) {
	p := NewWebPushCandidatePipeline(nil)
	p.Ingest(completionCandidate("e1:1"), ProjectionIngestApplied)
	if got := p.Drain(); len(got) != 0 {
		t.Fatalf("nil store must produce no candidates: %+v", got)
	}
}

func TestCandidatePipelineQueueFullFailsClosed(t *testing.T) {
	p, _ := newCandidatePipelineForTest(t)
	// 填满有界队列（容量 256），第 257 个不同 key candidate 必须 fail closed 且不 panic。
	for i := 0; i < webPushCandidateQueueCapacity+10; i++ {
		c := completionCandidate("e1:" + candidateTestItoa(i))
		c.NotificationKey = "codex|cand-1|turn-" + candidateTestItoa(i) + "|completed"
		p.Ingest(c, ProjectionIngestApplied)
	}
	got := p.Drain()
	if len(got) != webPushCandidateQueueCapacity {
		t.Fatalf("queued = %d, want exactly capacity %d", len(got), webPushCandidateQueueCapacity)
	}
	_, _, queueFull, _, _, _, _ := p.Stats()
	if queueFull != 10 {
		t.Fatalf("queue_full counter = %d, want 10", queueFull)
	}
}

func TestCandidatePipelineIngestNeverBlocks(t *testing.T) {
	p, _ := newCandidatePipelineForTest(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			c := completionCandidate("e1:" + candidateTestItoa(i))
			c.NotificationKey = "codex|cand-1|turn-" + candidateTestItoa(i) + "|completed"
			p.Ingest(c, ProjectionIngestApplied)
		}
	}()
	select {
	case <-done:
	case <-timeoutChan():
		t.Fatal("Ingest blocked — violates the publisher-lock non-blocking contract")
	}
}

func candidateTestItoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func timeoutChan() <-chan time.Time {
	return time.After(10 * time.Second)
}

// Publisher-level integration: a live timeline event with PushIntent produces a
// candidate even with ZERO online targets (web push exists precisely for the
// offline PWA), while the same intent on a control-plane publish is dropped.
func TestPublisherPushIntentZeroTargetsStillCandidates(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_pub_test")

	publisher := NewEventPublisher("epoch-1")
	publisher.SetProjectionKernel(NewProjectionKernel(NewProjectionReducer(), nil))
	publisher.SetWebPushCandidateSink(pipeline)

	// No broadcaster targets registered at all — zero online WebSocket clients.
	publisher.PublishLogical(LogicalEvent{
		BackendID: "codex",
		SessionID: "pub-1",
		Event:     "turn_completed",
		Data:      map[string]interface{}{"turnId": "t1", "itemId": "t1"},
		PushIntent: &PushIntent{
			Kind:            WebPushKindCompletion,
			NotificationKey: "codex|pub-1|t1|completed",
			AnchorKind:      "turn",
			AnchorID:        "t1",
		},
	})
	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("zero-online candidate = %d, want 1 (offline PWA is the audience)", len(got))
	}
	if got[0].EventID == "" || got[0].NotificationKey != "codex|pub-1|t1|completed" {
		t.Fatalf("candidate = %+v", got[0])
	}
	if got[0].BridgeID != "brg_pub_test" {
		t.Fatalf("BridgeID = %q", got[0].BridgeID)
	}
}

// A PushIntent riding a control-plane publish must be dropped (producer contract:
// catalog/control events are not notification producers; there is no kernel ingest
// on that path, so the sink never fires).
func TestPublisherPushIntentOnControlPlaneDropped(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)

	publisher := NewEventPublisher("epoch-1")
	publisher.SetProjectionKernel(NewProjectionKernel(NewProjectionReducer(), nil))
	publisher.SetWebPushCandidateSink(pipeline)

	publisher.PublishControlPlane(LogicalEvent{
		BackendID:  "codex",
		SessionID:  "pub-2",
		Event:      "sessions_changed",
		Data:       map[string]interface{}{"generation": 1},
		PushIntent: &PushIntent{Kind: WebPushKindCompletion, NotificationKey: "codex|pub-2|x|completed"},
	})
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("control-plane PushIntent must not produce a candidate: %+v", got)
	}
}
