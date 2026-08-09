package gobridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type relayOutboundClass uint8

const (
	relayOutboundControl relayOutboundClass = iota
	relayOutboundInteractive
	relayOutboundMetadata
	relayOutboundNormal
	relayOutboundBulk
	relayOutboundClassCount

	relayOutboundQueueFrames = 4096
	relayOutboundQueueBytes  = 64 << 20
	relayOutboundBulkFrames  = 2048
	relayOutboundBulkBytes   = 48 << 20
	relayControlBurstCap     = 8
	relayChunkTargetBytes    = 32 << 10
	relayChunkMinimumBytes   = 16 << 10
	relayChunkMaximumCount   = 1024
	relayLogicalMaximumBytes = 50 << 20
	relayBulkCursorMaxAge    = 2 * time.Minute
)

var errRelayBulkQueueOverflow = errors.New("relay bulk queue overflow")

var (
	relayResultBypassWriter           atomic.Uint64
	relayBulkPreemptions              atomic.Uint64
	relayBulkSuperseded               atomic.Uint64
	relayBulkSupersededBeforeSubmit   atomic.Uint64
	relayOutboundQueueOverflow        atomic.Uint64
	relayOutboundQueueFramesHighWater atomic.Int64
	relayOutboundQueueBytesHighWater  atomic.Int64
)

// relayUnifiedWriterV1 is a release-window switch. It is sampled when a new
// secure Relay device epoch is created; an established epoch never switches
// writer paths. Remove the legacy branch after the observation window.
var relayUnifiedWriterV1 = true

type relayOutboundJob struct {
	conn            *RelayDeviceConn
	payload         []byte
	contentEncoding string
	class           relayOutboundClass
	admittedAt      time.Time
	done            chan error
	cursor          *relayChunkCursor
}

type relayChunkCursor struct {
	groupID           string
	nextIndex         uint32
	count             uint32
	chunkBytes        int
	handle            *OutboundBulkHandle
	sessionID         string
	sessionGeneration uint64
	channelGeneration uint64
	expiresAt         time.Time
	bulkCorrelationID string // R1.4：read_file_v2 correlated chunk 的 request-aware 绑定（空 = base chunk）
	requestID         string // R1.5：read_file_v2 chunked result 的 requestId（complete 时清理 cancel handle）
}

// relayOutboundWriter is the only owner of Relay application-data writes for
// one MacBridge-to-Relay WebSocket. WebSocket WriteControl and the pre-writer
// online handshake are intentionally outside this owner.
type relayOutboundWriter struct {
	mu                      sync.Mutex
	queues                  [relayOutboundClassCount][]*relayOutboundJob
	activeBulkGroupByDevice map[string]string
	queueFrames             int
	queueBytes              int
	bulkFrames              int
	bulkBytes               int
	lastDevice              [relayOutboundClassCount]string
	controlBurst            int
	serviceCursor           int
	wake                    chan struct{}
	stop                    chan struct{}
	once                    sync.Once
}

func newRelayOutboundWriter() *relayOutboundWriter {
	w := &relayOutboundWriter{
		activeBulkGroupByDevice: make(map[string]string),
		wake:                    make(chan struct{}, 1),
		stop:                    make(chan struct{}),
	}
	slog.Info("relay unified writer started",
		"relay_result_bypass_writer", relayResultBypassWriter.Load(),
		"relay_outbound_queue_overflow", relayOutboundQueueOverflow.Load(),
		"relay_queue_frames_high_water", relayOutboundQueueFramesHighWater.Load(),
		"relay_queue_bytes_high_water", relayOutboundQueueBytesHighWater.Load())
	go w.run()
	return w
}

func (w *relayOutboundWriter) enqueue(job *relayOutboundJob) error {
	if job == nil || job.conn == nil {
		return fmt.Errorf("relay outbound job is invalid")
	}
	if job.class >= relayOutboundClassCount {
		job.class = relayOutboundNormal
	}
	job.admittedAt = time.Now()
	job.done = make(chan error, 1)

	w.mu.Lock()
	select {
	case <-w.stop:
		w.mu.Unlock()
		return fmt.Errorf("relay outbound writer is closed")
	default:
	}
	if w.queueFrames+1 > relayOutboundQueueFrames || w.queueBytes+len(job.payload) > relayOutboundQueueBytes {
		relayOutboundQueueOverflow.Add(1)
		slog.Error("relay outbound queue overflow", "relay_queue_frames", w.queueFrames, "relay_queue_bytes", w.queueBytes, "relay_outbound_class", job.class)
		w.mu.Unlock()
		return fmt.Errorf("relay outbound queue overflow")
	}
	w.queues[job.class] = append(w.queues[job.class], job)
	w.queueFrames++
	w.queueBytes += len(job.payload)
	updateRelayHighWater(&relayOutboundQueueFramesHighWater, int64(w.queueFrames))
	updateRelayHighWater(&relayOutboundQueueBytesHighWater, int64(w.queueBytes))
	w.mu.Unlock()

	select {
	case w.wake <- struct{}{}:
	default:
	}
	select {
	case err := <-job.done:
		return err
	case <-w.stop:
		return fmt.Errorf("relay outbound writer closed before delivery")
	case <-time.After(relayWriteTimeout):
		return fmt.Errorf("relay outbound delivery timeout")
	}
}

func (w *relayOutboundWriter) admitBulk(job *relayOutboundJob) error {
	if job == nil || job.conn == nil || job.cursor == nil || job.cursor.handle == nil {
		return fmt.Errorf("relay bulk job is invalid")
	}
	job.class = relayOutboundBulk
	job.admittedAt = time.Now()
	w.mu.Lock()
	select {
	case <-w.stop:
		w.mu.Unlock()
		return fmt.Errorf("relay outbound writer is closed")
	default:
	}
	if w.queueFrames+1 > relayOutboundQueueFrames || w.queueBytes+len(job.payload) > relayOutboundQueueBytes ||
		w.bulkFrames+1 > relayOutboundBulkFrames || w.bulkBytes+len(job.payload) > relayOutboundBulkBytes {
		relayOutboundQueueOverflow.Add(1)
		slog.Warn("relay bulk queue overflow", "relay_queue_frames", w.queueFrames, "relay_queue_bytes", w.queueBytes, "relay_bulk_frames", w.bulkFrames, "relay_bulk_bytes", w.bulkBytes)
		w.mu.Unlock()
		return errRelayBulkQueueOverflow
	}
	w.queues[relayOutboundBulk] = append(w.queues[relayOutboundBulk], job)
	w.queueFrames++
	w.queueBytes += len(job.payload)
	w.bulkFrames++
	w.bulkBytes += len(job.payload)
	updateRelayHighWater(&relayOutboundQueueFramesHighWater, int64(w.queueFrames))
	updateRelayHighWater(&relayOutboundQueueBytesHighWater, int64(w.queueBytes))
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return nil
}

func (w *relayOutboundWriter) run() {
	for {
		select {
		case <-w.stop:
			w.failQueued(fmt.Errorf("relay outbound writer closed"))
			return
		case <-w.wake:
			for {
				job := w.pop()
				if job == nil {
					break
				}
				err, complete := w.writeSelected(job)
				if complete {
					w.completeBulkGroup(job)
					if job.cursor != nil && job.cursor.sessionID != "" {
						job.conn.completeBulkHandle(job.cursor.sessionID, job.cursor.handle)
					}
					// R1.5：read_file_v2 chunk group 完成 → 清理 requestId→handle（cancel 不再可命中）。
					if job.cursor != nil && job.cursor.requestID != "" {
						job.conn.completeRequestBulkHandle(job.cursor.requestID, job.cursor.handle)
					}
					// R1.4：correlated chunk group 完成（成功/错误/超时）→ retire correlation，
					// 进入 retired 窗口（防 reuse）。conn 关闭时整个 registry 随之销毁。
					if job.cursor != nil && job.cursor.bulkCorrelationID != "" {
						job.conn.bulkCorrelations.Retire(job.cursor.bulkCorrelationID)
					}
					if job.done != nil {
						job.done <- err
					}
					if err != nil {
						_ = job.conn.Close()
					}
				} else {
					w.requeueBulk(job)
				}
			}
		}
	}
}

func (w *relayOutboundWriter) writeSelected(job *relayOutboundJob) (error, bool) {
	queueWait := time.Since(job.admittedAt)
	writtenAt := time.Now()
	if job.cursor == nil {
		err := job.conn.writeLogicalFrame(job.payload, job.contentEncoding, nil)
		wire := time.Since(writtenAt)
		slog.Info("relay outbound delivered", "relay_queue_wait_ms", durationMillis(queueWait), "socket_send_ms", durationMillis(wire), "relay_outbound_total_ms", durationMillis(time.Since(job.admittedAt)), "relay_control_wait_ms", controlWaitMillis(job.class, queueWait))
		return err, true
	}
	cursor := job.cursor
	// R1.5：在写 index0 前 CAS active→committed 声明 committedToWriter 边界。若 cancel 已赢得 CAS
	// （state=cancelled）则 MarkIndex0Committed 返回 false → 跳过 group（cancelled）。这把 cancel 与
	// index0-commit 在单一 atomic 上线性化（plan §3.6.4「cancel唯一原子状态机」）。
	if cursor.nextIndex == 0 && !cursor.handle.MarkIndex0Committed() {
		relayBulkSuperseded.Add(1)
		return nil, true
	}
	if time.Now().After(cursor.expiresAt) {
		relayBulkSuperseded.Add(1)
		if cursor.nextIndex > 0 {
			return fmt.Errorf("relay chunk group expired after transmission began"), true
		}
		return nil, true
	}
	if job.conn.channelGeneration() != cursor.channelGeneration || job.conn.isClosed() {
		relayBulkSuperseded.Add(1)
		return nil, true
	}
	start := int(cursor.nextIndex) * cursor.chunkBytes
	if start >= len(job.payload) {
		return nil, true
	}
	end := start + cursor.chunkBytes
	if end > len(job.payload) {
		end = len(job.payload)
	}
	metadata := &RelayChunkMetadata{GroupID: cursor.groupID, Index: cursor.nextIndex, Count: cursor.count, BulkCorrelationID: cursor.bulkCorrelationID}
	err := job.conn.writeLogicalFrame(job.payload[start:end], job.contentEncoding, metadata)
	wire := time.Since(writtenAt)
	slog.Info(
		"relay chunk delivered",
		"requestId", cursor.requestID,
		"groupId", metadata.GroupID,
		"bulkCorrelationId", metadata.BulkCorrelationID,
		"relay_queue_wait_ms", durationMillis(queueWait),
		"socket_send_ms", durationMillis(wire),
		"relay_chunk_wire_ms", durationMillis(wire),
		"relay_outbound_total_ms", durationMillis(time.Since(job.admittedAt)),
		"relay_chunk_count", cursor.count,
		"chunk_index", metadata.Index,
	)
	if err != nil {
		return err, true
	}
	cursor.nextIndex++
	return nil, cursor.nextIndex >= cursor.count
}

func controlWaitMillis(class relayOutboundClass, wait time.Duration) float64 {
	if class == relayOutboundControl {
		return durationMillis(wait)
	}
	return 0
}

func (w *relayOutboundWriter) requeueBulk(job *relayOutboundJob) {
	w.mu.Lock()
	w.queues[relayOutboundBulk] = append(w.queues[relayOutboundBulk], job)
	w.queueFrames++
	w.queueBytes += len(job.payload)
	w.bulkFrames++
	w.bulkBytes += len(job.payload)
	higherReady := false
	for class := relayOutboundControl; class < relayOutboundBulk; class++ {
		if len(w.queues[class]) != 0 {
			higherReady = true
			break
		}
	}
	w.mu.Unlock()
	if higherReady {
		relayBulkPreemptions.Add(1)
	}
}

func (w *relayOutboundWriter) pop() *relayOutboundJob {
	w.mu.Lock()
	defer w.mu.Unlock()
	nonControlReady := false
	for class := relayOutboundInteractive; class < relayOutboundClassCount; class++ {
		if len(w.queues[class]) != 0 {
			nonControlReady = true
			break
		}
	}
	if len(w.queues[relayOutboundControl]) != 0 && (w.controlBurst < relayControlBurstCap || !nonControlReady) {
		w.controlBurst++
		return w.popClassLocked(relayOutboundControl, false)
	}
	serviceOrder := [...]relayOutboundClass{
		relayOutboundInteractive, relayOutboundInteractive, relayOutboundMetadata, relayOutboundNormal,
	}
	for range serviceOrder {
		class := serviceOrder[w.serviceCursor%len(serviceOrder)]
		w.serviceCursor = (w.serviceCursor + 1) % len(serviceOrder)
		if len(w.queues[class]) != 0 {
			w.controlBurst = 0
			return w.popClassLocked(class, true)
		}
	}
	if len(w.queues[relayOutboundBulk]) != 0 {
		w.controlBurst = 0
		return w.popClassLocked(relayOutboundBulk, true)
	}
	if len(w.queues[relayOutboundControl]) != 0 {
		w.controlBurst++
		return w.popClassLocked(relayOutboundControl, false)
	}
	return nil
}

func (w *relayOutboundWriter) popClassLocked(class relayOutboundClass, perDevice bool) *relayOutboundJob {
	queue := w.queues[class]
	index := 0
	if class == relayOutboundBulk {
		index = w.selectBulkIndexLocked(queue)
		if index < 0 {
			return nil
		}
	} else if perDevice && len(queue) > 1 && w.lastDevice[class] != "" {
		for candidate, job := range queue {
			if job.conn.deviceID != w.lastDevice[class] {
				index = candidate
				break
			}
		}
	}
	job := queue[index]
	if class == relayOutboundBulk && job.cursor != nil && w.activeBulkGroupByDevice[job.conn.deviceID] == "" {
		w.activeBulkGroupByDevice[job.conn.deviceID] = job.cursor.groupID
	}
	w.queues[class] = append(queue[:index], queue[index+1:]...)
	w.lastDevice[class] = job.conn.deviceID
	w.queueFrames--
	w.queueBytes -= len(job.payload)
	if class == relayOutboundBulk {
		w.bulkFrames--
		w.bulkBytes -= len(job.payload)
	}
	return job
}

func (w *relayOutboundWriter) selectBulkIndexLocked(queue []*relayOutboundJob) int {
	eligible := func(job *relayOutboundJob) bool {
		if job.cursor == nil {
			return true
		}
		active := w.activeBulkGroupByDevice[job.conn.deviceID]
		return active == "" || active == job.cursor.groupID
	}
	for index, job := range queue {
		if eligible(job) && job.conn.deviceID != w.lastDevice[relayOutboundBulk] {
			return index
		}
	}
	for index, job := range queue {
		if eligible(job) {
			return index
		}
	}
	return -1
}

func (w *relayOutboundWriter) completeBulkGroup(job *relayOutboundJob) {
	if job == nil || job.conn == nil || job.cursor == nil {
		return
	}
	w.mu.Lock()
	if w.activeBulkGroupByDevice[job.conn.deviceID] == job.cursor.groupID {
		delete(w.activeBulkGroupByDevice, job.conn.deviceID)
	}
	w.mu.Unlock()
}

func (w *relayOutboundWriter) failQueued(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for class := relayOutboundControl; class < relayOutboundClassCount; class++ {
		for _, job := range w.queues[class] {
			if job.cursor != nil {
				job.cursor.handle.Cancel()
			}
			if job.done != nil {
				job.done <- err
			}
		}
		w.queues[class] = nil
	}
	w.queueFrames = 0
	w.queueBytes = 0
	w.bulkFrames = 0
	w.bulkBytes = 0
}

func (w *relayOutboundWriter) close() {
	w.once.Do(func() { close(w.stop) })
}

func updateRelayHighWater(counter *atomic.Int64, value int64) {
	for current := counter.Load(); value > current; current = counter.Load() {
		if counter.CompareAndSwap(current, value) {
			return
		}
	}
}

func classifyRelayRequest(method string) relayOutboundClass {
	switch method {
	case "hello", "ping", "abort_generation", "recovery_applied":
		return relayOutboundControl
	case "send_message", "resolve_permission", "permission_response", "question_reply", "question_reject":
		return relayOutboundInteractive
	case "list_models", "list_permission_modes", "get_git_context", "get_todos", "list_agents", "list_providers":
		return relayOutboundMetadata
	case "get_session_messages", "get_session_projection":
		return relayOutboundBulk
	// R1.3（§3.6.4）：read_file_v2 的结果可能很大（最高 2 MiB），
	// 走 bulk 路径以复用 gzip + relay_chunks_v1 公平分块，避免单巨型 Normal 帧在弱网
	// Relay 上饿死 text_delta/permission 等交互帧。base chunk path（无 correlation）；
	// request-aware progress（bulkCorrelationId）属于 R1.4。inbound 请求本身很小，
	// 归为 bulk 仅影响调度优先级（低于 interactive），不延迟其 filepool 派发。
	case "read_file_v2":
		return relayOutboundBulk
	default:
		return relayOutboundNormal
	}
}

func classifyRelayEvent(event string) relayOutboundClass {
	switch event {
	case "recovery_barrier", "recovery_complete", "sync_invalidate":
		return relayOutboundControl
	case "text_delta", "thinking_delta", "tool_content", "message_content", "user_message", "turn_started", "turn_completed", "permission_asked", "question_asked", "projection_patch", "projection_snapshot":
		return relayOutboundInteractive
	case "session_updated", "session_state_changed", "session_status", "todos_updated", "permission_mode_changed", "model_changed", "git_branch_changed":
		return relayOutboundMetadata
	default:
		return relayOutboundNormal
	}
}

func classifyRelayPayload(payload []byte) relayOutboundClass {
	var header struct {
		Type  string `json:"type"`
		Event string `json:"event"`
	}
	if json.Unmarshal(payload, &header) != nil {
		return relayOutboundNormal
	}
	switch header.Type {
	case "hello_ack", "hello_error", "ping", "pong", "recovery_barrier", "recovery_complete":
		return relayOutboundControl
	case "event":
		return classifyRelayEvent(header.Event)
	default:
		return relayOutboundNormal
	}
}
