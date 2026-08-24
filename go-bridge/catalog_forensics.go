package gobridge

// catalog_forensics.go —— discovery 取证 observer（v5 取证执行稿 §3 冻结契约）。
//
// 临时、bridge 内部、只读 control-plane observer：
//   - 默认关闭（GO_BRIDGE_CODEX_CATALOG_TRACE=1 显式开启）；
//   - 样本来自 fingerprint 实际使用的同一份 wire slice，绝不为本观测额外调用
//     thread/list（同源硬约束）；
//   - 不写 timeline/Projection Kernel，不增加 writer，不成为 catalog 数据源；
//   - 不修改 fingerprint、seen、fence、generation、投递、轮询 cadence 或 coalescing；
//   - 所有错误被边界捕获转成有界 observerError，不得使 discovery 失败；
//   - 有界：maxSamples（默认 256，>512 钳制 512）+ 总量 1 MiB，任一事件写入前
//     原子检查，先到者停止（越界事件本身不写入）并输出单个 run_summary；为
//     run_summary 预留空间，证据总量（含终报）恒 ≤ maxBytes。
//
// 输出：结构化日志事件 msg=go-bridge: catalog_forensics，event=紧凑 JSON
// （schema catalog-forensics.v1）。提取入口 scripts/codex-web-phase0/extract_catalog_forensics.sh。

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	forensicsSchemaVersion = "catalog-forensics.v1"

	forensicsMaxSamplesDefault = 256
	forensicsMaxSamplesCap     = 512
	forensicsMaxBytes          = 1 << 20 // 1 MiB
	// forensicsBudgetReserve 为 run_summary 预留字节：事件预算按 maxBytes-reserve
	// 判定，保证导出的证据总量（含唯一一条 run_summary）恒 ≤ maxBytes。
	forensicsBudgetReserve = 4096

	forensicsTraceEnv   = "GO_BRIDGE_CODEX_CATALOG_TRACE"
	forensicsMaxEnv     = "GO_BRIDGE_CODEX_CATALOG_TRACE_MAX_SAMPLES"
	forensicsLogMessage = "go-bridge: catalog_forensics"
)

// forensicsTrigger 是第一轮只允许的 trigger 枚举（§3.3）；禁止伪造 lifecycle/manual。
type forensicsTrigger string

const (
	forensicsTriggerSeed          forensicsTrigger = "seed"
	forensicsTriggerPeriodicTick  forensicsTrigger = "periodic_tick"
	forensicsTriggerHeadChanged   forensicsTrigger = "head_changed"
	forensicsTriggerCatalogSignal forensicsTrigger = "catalog_signal_coalesced"
)

type forensicsCorpusKind string

const (
	forensicsCorpusHead          forensicsCorpusKind = "head"
	forensicsCorpusAuthoritative forensicsCorpusKind = "authoritative"
)

// fieldChangeMask 位（§3.4）。
const (
	forensicsMaskAdded     uint8 = 1 << iota // 新 row（rawID 不在上一份同 corpus 样本）
	forensicsMaskRemoved                     // 旧 row 消失
	forensicsMaskIndex                       // 序位变化
	forensicsMaskUpdatedAt                   // updatedAt 变化（仅 authoritative）
	forensicsMaskDirectory
	forensicsMaskProject
	forensicsMaskTitle
)

type forensicsRecordKind string

const (
	forensicsRecordSampleSummary forensicsRecordKind = "sample_summary"
	forensicsRecordRowDiff       forensicsRecordKind = "row_diff"
	forensicsRecordRunSummary    forensicsRecordKind = "run_summary"
)

// forensicsObserverError 是有界枚举；绝不记录可能携带 payload/path 的原始 error 串。
type forensicsObserverError string

const (
	forensicsErrorNone         forensicsObserverError = "none"
	forensicsErrorEncodeFailed forensicsObserverError = "encode_failed"
	forensicsErrorLimitReached forensicsObserverError = "limit_reached"
	forensicsErrorWriteFailed  forensicsObserverError = "write_failed"
	forensicsErrorDropped      forensicsObserverError = "dropped"
)

// forensicsConfig 是测试可注入的配置向量。
type forensicsConfig struct {
	enabled    bool
	maxSamples int
	maxBytes   int64
	now        func() time.Time
}

func forensicsConfigFromEnv() forensicsConfig {
	cfg := forensicsConfig{maxSamples: forensicsMaxSamplesDefault, maxBytes: forensicsMaxBytes, now: time.Now}
	if os.Getenv(forensicsTraceEnv) != "1" {
		cfg.enabled = false
		return cfg
	}
	cfg.enabled = true
	if v := os.Getenv(forensicsMaxEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.maxSamples = n
		}
	}
	if cfg.maxSamples > forensicsMaxSamplesCap {
		cfg.maxSamples = forensicsMaxSamplesCap
	}
	return cfg
}

// forensicsFactory 可由测试替换；生产从环境变量构造（默认关闭 → nil）。
var forensicsFactory = func() *forensicsRun {
	cfg := forensicsConfigFromEnv()
	if !cfg.enabled {
		return nil
	}
	return newForensicsRun(cfg)
}

// forensicsRow 是内存中的脱敏行状态（只用于同 run 内相邻样本 diff；绝不持久化）。
type forensicsRow struct {
	index     int
	updatedAt int64
	dirHash   uint64
	projHash  uint64
	titleHash uint64
}

// forensicsSample 是 Capture 的产物：与 fingerprint 同源的有界脱敏内存对象。
type forensicsSample struct {
	sampleID string
	backend  string // 与 fingerprint 同一目录的来源 backend；diff 只允许同 backend 相邻样本
	corpus   forensicsCorpusKind
	trigger  forensicsTrigger
	rowCount int
	rawCount int
	rows     []forensicsRow
	ids      []string // 顺序同 rows；仅内存 diff 使用（不输出）
	monotMs  int64
}

type forensicsRowDiff struct {
	rowKeyHmac       string
	fieldMask        uint8
	index            int
	updatedAtDeltaMs int64
}

// forensicsRun 是一次取证的运行态。HMAC key 只存内存。
type forensicsRun struct {
	mu         sync.Mutex
	cfg        forensicsConfig
	runID      string
	hmacKey    []byte
	samples    int
	bytes      int64
	dropped    int
	stopped    bool
	stoppedErr forensicsObserverError
	startedMs  int64
	prev       map[string]map[string]forensicsRow
}

func newForensicsRun(cfg forensicsConfig) *forensicsRun {
	return &forensicsRun{
		cfg:       cfg,
		runID:     randomHex(16),
		hmacKey:   randomHexBytes(24),
		startedMs: cfg.now().UnixNano() / int64(time.Millisecond),
		prev:      map[string]map[string]forensicsRow{},
	}
}

// prevKey 限定相邻样本 diff 属于同一 backend 且同一 corpus；不同 backend 的
// catalog 可能不同（本机实测 codex 与 codex-web 的 rawCount/指纹不同），
// 跨 backend 比较会伪装出整表 changed（v5 §3.2 同源约束）。
func prevKey(backend string, corpus forensicsCorpusKind) string {
	return backend + "\x00" + string(corpus)
}

func randomHex(n int) string {
	return hex.EncodeToString(randomHexBytes(n))
}

func randomHexBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("catalog_forensics: crypto/rand failure: %v", err))
	}
	return b
}

// hashWireField 生成同 run 内字段差异比较用的短摘要（不输出）。
func hashWireField(v interface{}) uint64 {
	h := fnv.New64a()
	switch t := v.(type) {
	case string:
		_, _ = h.Write([]byte(t))
	case int64:
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(t))
		_, _ = h.Write(buf[:])
	case float64:
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(t))
		_, _ = h.Write(buf[:])
	}
	return h.Sum64()
}

// forensicsCaptureHook 仅供定向单测注入 panic/错误；生产恒为 nil。
var forensicsCaptureHook func()

// capture 从 fingerprint 同源 wire slice 生成内存样本。panic 被边界转成 dropped
// （运行停止并输出单个 run_summary），不影响调用方。
func (r *forensicsRun) capture(backend string, corpus forensicsCorpusKind, trigger forensicsTrigger, wire []map[string]interface{}, rawCount int) (sample *forensicsSample) {
	if r == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			r.mu.Lock()
			r.forensicDropLocked(forensicsErrorDropped)
			r.mu.Unlock()
			sample = nil
		}
	}()
	if forensicsCaptureHook != nil {
		forensicsCaptureHook()
	}
	sample = &forensicsSample{
		sampleID: randomHex(12),
		backend:  backend,
		corpus:   corpus,
		trigger:  trigger,
		rowCount: len(wire),
		rawCount: rawCount,
		rows:     make([]forensicsRow, len(wire)),
		ids:      make([]string, len(wire)),
		monotMs:  r.cfg.now().UnixNano() / int64(time.Millisecond),
	}
	for index, item := range wire {
		id, _ := item["id"].(string)
		row := forensicsRow{index: index}
		if corpus == forensicsCorpusAuthoritative {
			if ts, ok := item["updatedAtMillis"].(int64); ok {
				row.updatedAt = ts
			}
			row.dirHash = hashWireField(item["directory"])
			row.projHash = hashWireField(item["projectId"])
			row.titleHash = hashWireField(item["title"])
		}
		sample.ids[index] = id
		sample.rows[index] = row
	}
	return sample
}

// commit 在原有状态推进完成后调用：补齐 fingerprint/generation 并 best-effort 输出。
func (r *forensicsRun) commit(s *forensicsSample, fingerprint string, genBefore, genAfter uint64, correlationID string) {
	if s == nil || r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		r.dropped++
		return
	}
	key := prevKey(s.backend, s.corpus)
	prev, hasPrev := r.prev[key]
	next := make(map[string]forensicsRow, len(s.ids))
	diffs := make([]forensicsRowDiff, 0)
	// 无上一份（首个同 backend 同 corpus 样本）：不产出 row_diff（§3.4「相对上一份」为空）。
	if hasPrev {
		for index, id := range s.ids {
			rowKey := r.rowKeyHMAC(id)
			row := s.rows[index]
			if prevRow, ok := prev[rowKey]; ok {
				mask := uint8(0)
				if prevRow.index != row.index {
					mask |= forensicsMaskIndex
				}
				if s.corpus == forensicsCorpusAuthoritative {
					if prevRow.updatedAt != row.updatedAt {
						mask |= forensicsMaskUpdatedAt
					}
					if prevRow.dirHash != row.dirHash {
						mask |= forensicsMaskDirectory
					}
					if prevRow.projHash != row.projHash {
						mask |= forensicsMaskProject
					}
					if prevRow.titleHash != row.titleHash {
						mask |= forensicsMaskTitle
					}
				}
				if mask != 0 {
					diffs = append(diffs, forensicsRowDiff{
						rowKeyHmac:       rowKey,
						fieldMask:        mask,
						index:            index,
						updatedAtDeltaMs: row.updatedAt - prevRow.updatedAt,
					})
				}
			} else {
				diffs = append(diffs, forensicsRowDiff{rowKeyHmac: rowKey, fieldMask: forensicsMaskAdded, index: index})
			}
			next[rowKey] = row
		}
		for rowKey, prevRow := range prev {
			if _, ok := next[rowKey]; !ok {
				diffs = append(diffs, forensicsRowDiff{rowKeyHmac: rowKey, fieldMask: forensicsMaskRemoved, index: prevRow.index})
			}
		}
	} else {
		for index, id := range s.ids {
			next[r.rowKeyHMAC(id)] = s.rows[index]
		}
	}
	if !r.emitSampleSummary(s, fingerprint, genBefore, genAfter, correlationID, len(diffs)) {
		r.commitFailedLocked()
		return
	}
	for _, d := range diffs {
		if !r.emitRowDiff(s, d, fingerprint, genBefore, genAfter) {
			r.commitFailedLocked()
			return
		}
	}
	r.prev[key] = next
}

// commitFailedLocked 是单次提交发射失败的边界（调用方持锁）：预算拒绝时 run 已
// 由 emit 停止并输出过 run_summary，这里仅计数被拒事件；其余失败（编码）按
// dropped 语义停止并输出 run_summary。
func (r *forensicsRun) commitFailedLocked() {
	if r.stopped {
		r.dropped++
		return
	}
	r.forensicDropLocked(forensicsErrorEncodeFailed)
}

// rowKeyHMAC 由稳定 raw ID 计算；key/salt 不写日志与证据包，跨 run 不可关联。
func (r *forensicsRun) rowKeyHMAC(id string) string {
	m := hmac.New(sha256.New, r.hmacKey)
	_, _ = m.Write([]byte(id))
	return hex.EncodeToString(m.Sum(nil))
}

func (r *forensicsRun) emitSampleSummary(s *forensicsSample, fingerprint string, genBefore, genAfter uint64, correlationID string, diffCount int) bool {
	event := map[string]interface{}{
		"schemaVersion":           forensicsSchemaVersion,
		"runId":                   r.runID,
		"sampleId":                s.sampleID,
		"correlationId":           nullableString(correlationID),
		"corpusKind":              string(s.corpus),
		"triggerKind":             string(s.trigger),
		"recordKind":              string(forensicsRecordSampleSummary),
		"monotonicOffsetMs":       s.monotMs - r.startedMs,
		"rowCount":                s.rowCount,
		"rawCount":                s.rawCount,
		"fingerprint":             fingerprint,
		"catalogGenerationBefore": genBefore,
		"catalogGenerationAfter":  genAfter,
		"rowKeyHmac":              nil,
		"fieldChangeMask":         nil,
		"index":                   nil,
		"updatedAtDeltaMs":        nil,
		"observerError":           string(forensicsErrorNone),
		"droppedCount":            nil,
	}
	if !r.emit(event, true) {
		return false
	}
	return true
}

func (r *forensicsRun) emitRowDiff(s *forensicsSample, d forensicsRowDiff, fingerprint string, genBefore, genAfter uint64) bool {
	event := map[string]interface{}{
		"schemaVersion":           forensicsSchemaVersion,
		"runId":                   r.runID,
		"sampleId":                s.sampleID,
		"correlationId":           nil,
		"corpusKind":              string(s.corpus),
		"triggerKind":             string(s.trigger),
		"recordKind":              string(forensicsRecordRowDiff),
		"monotonicOffsetMs":       s.monotMs - r.startedMs,
		"rowCount":                nil,
		"rawCount":                nil,
		"fingerprint":             fingerprint,
		"catalogGenerationBefore": genBefore,
		"catalogGenerationAfter":  genAfter,
		"rowKeyHmac":              d.rowKeyHmac,
		"fieldChangeMask":         int(d.fieldMask),
		"index":                   d.index,
		"updatedAtDeltaMs":        nullableInt(d.updatedAtDeltaMs),
		"observerError":           string(forensicsErrorNone),
		"droppedCount":            nil,
	}
	return r.emit(event, false)
}

func (r *forensicsRun) emitRunSummary(err forensicsObserverError) {
	event := map[string]interface{}{
		"schemaVersion":           forensicsSchemaVersion,
		"runId":                   r.runID,
		"sampleId":                nil,
		"correlationId":           nil,
		"corpusKind":              nil,
		"triggerKind":             nil,
		"recordKind":              string(forensicsRecordRunSummary),
		"monotonicOffsetMs":       r.cfg.now().UnixNano()/int64(time.Millisecond) - r.startedMs,
		"rowCount":                nil,
		"rawCount":                nil,
		"fingerprint":             nil,
		"catalogGenerationBefore": nil,
		"catalogGenerationAfter":  nil,
		"rowKeyHmac":              nil,
		"fieldChangeMask":         nil,
		"index":                   nil,
		"updatedAtDeltaMs":        nil,
		"observerError":           string(err),
		"droppedCount":            r.dropped,
	}
	r.writeEvent(event)
}

// emit 逐事件原子预算写入：任一事件写入前先验证字节与样本预算均未达上限，
// 越界时本事件不写入并立即停止（输出唯一 run_summary=limit_reached）。样本数
// 上限只限制新的 sample_summary，当前样本的 row_diff 序列不因此被截断；
// run_summary 由 forensicsBudgetReserve 预留，故导出总量恒 ≤ maxBytes。
func (r *forensicsRun) emit(event map[string]interface{}, isSampleSummary bool) bool {
	encoded, err := json.Marshal(event)
	if err != nil {
		return false
	}
	sz := int64(len(encoded) + 1) // + '\n'
	if !r.budgetCheckLocked(sz, isSampleSummary) {
		return false
	}
	r.bytes += sz
	if isSampleSummary {
		r.samples++
	}
	return r.writeEventRaw(encoded)
}

// budgetCheckLocked 调用方持锁。
func (r *forensicsRun) budgetCheckLocked(sz int64, isSampleSummary bool) bool {
	if r.stopped {
		return false
	}
	if (isSampleSummary && r.samples >= r.cfg.maxSamples) || r.bytes+sz > r.budgetRoomLocked() {
		r.forensicsStopLocked(forensicsErrorLimitReached)
		return false
	}
	return true
}

// budgetRoomLocked 是事件预算上限：maxBytes-预留。极小预算（测试向量）下预留
// 退化为四分之一，避免把事件预算清零。
func (r *forensicsRun) budgetRoomLocked() int64 {
	reserve := int64(forensicsBudgetReserve)
	if r.cfg.maxBytes-reserve < r.cfg.maxBytes/4 {
		reserve = r.cfg.maxBytes / 4
	}
	return r.cfg.maxBytes - reserve
}

// forensicsStopLocked 调用方持锁；仅首次停止输出唯一 run_summary。
func (r *forensicsRun) forensicsStopLocked(err forensicsObserverError) {
	if r.stopped {
		return
	}
	r.stopped = true
	r.stoppedErr = err
	r.emitRunSummary(err)
}

func (r *forensicsRun) writeEvent(event map[string]interface{}) {
	encoded, err := json.Marshal(event)
	if err != nil {
		return
	}
	_ = r.writeEventRaw(encoded)
}

func (r *forensicsRun) writeEventRaw(encoded []byte) bool {
	slog.Info(forensicsLogMessage, "event", string(encoded))
	return true
}

// forensicDropLocked 调用方持锁：被拒/失败事件计数 + 首次停止输出 run_summary。
func (r *forensicsRun) forensicDropLocked(err forensicsObserverError) {
	r.dropped++
	r.forensicsStopLocked(err)
}

// forensicsCtx 贯穿一次 authoritative 取样的取证上下文。sample 由 discovery 路径
// 的 Capture 填充，Commit 在原路径状态推进后执行。
type forensicsCtx struct {
	run         *forensicsRun
	backend     string
	trigger     forensicsTrigger
	correlation string
	sample      *forensicsSample
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(v int64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
