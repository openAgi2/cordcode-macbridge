package gobridge

// R1.3（§3.6.4）：read_file_v2 / legacy read_file 结果分类为 relayOutboundBulk，
// 复用 gzip + relay_chunks_v1 公平分块。证明：
//  1. chunks+gzip acked 且结果 > 阈值 → 分块 + gzip，重组后还原 result JSON；
//  2. 小结果（< 阈值）单帧不 chunk；
//  3. 未 ack chunks → 单帧（base 兼容）。

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// highEntropyContent 产生 ~n 字节高熵 ASCII（base64 of random bytes），gzip 后仍 > 阈值，
// 模拟真实源码/二进制（plan §3.6.4「2 MiB 高熵 read-file」）。零字节 payload 会被 gzip 压到
// chunk 阈值之下，不能证明 chunk 路径。
func highEntropyContent(n int) string {
	raw := make([]byte, n*3/4) // base64 膨胀 ~4/3
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawStdEncoding.EncodeToString(raw)
}

// newReadFileBulkTestConn 构造一个 outboundChunks+gzip 启用的 RelayDeviceConn，
// envelope 写入 out channel 供测试断言。
func newReadFileBulkTestConn(t *testing.T, chunks, gzip bool) (*RelayDeviceConn, chan RelayEnvelope) {
	t.Helper()
	key := make([]byte, 32)
	out := make(chan RelayEnvelope, 32)
	rc := NewRelayDeviceConn("device", "bridge", "route", 9, nil,
		append([]byte(nil), key...), nil,
		func(raw json.RawMessage) error {
			var env RelayEnvelope
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			out <- env
			return nil
		},
	)
	w := newRelayOutboundWriter()
	t.Cleanup(w.close)
	rc.setOutboundWriter(w)
	rc.mu.Lock()
	if chunks {
		rc.outboundChunks = rc.writer != nil
	}
	if gzip {
		rc.outboundGzip = true
	}
	rc.mu.Unlock()
	return rc, out
}

// collectResultFrames 排空 out，重组属于同一 chunk group 的帧，返回 (combinedPlain, sawChunk, contentEncoding)。
func collectResultFrames(t *testing.T, out chan RelayEnvelope, key []byte) ([]byte, bool, string) {
	t.Helper()
	var combined []byte
	var groupID string
	var count uint32
	var encoding string
	sawChunk := false
	deadline := time.After(2 * time.Second)
	for {
		select {
		case env := <-out:
			if env.Chunk != nil {
				sawChunk = true
				if groupID == "" {
					groupID, count = env.Chunk.GroupID, env.Chunk.Count
				}
				if env.Chunk.GroupID != groupID || env.Chunk.Count != count {
					t.Fatalf("chunk metadata drift: %+v", env.Chunk)
				}
			}
			if encoding == "" {
				encoding = env.ContentEncoding
			}
			aad, err := env.EncodeAAD()
			if err != nil {
				t.Fatal(err)
			}
			plain, err := OpenEnvelope(key, env.Counter, aad, env.Ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			combined = append(combined, plain...)
			if env.Chunk != nil && env.Chunk.Index+1 == env.Chunk.Count {
				return combined, sawChunk, encoding
			}
			if env.Chunk == nil {
				return combined, sawChunk, encoding
			}
		case <-deadline:
			t.Fatal("timed out waiting for result frames")
			return nil, false, ""
		}
	}
}

func TestReadFileV2ResultChunkedAndGzipped(t *testing.T) {
	if got := classifyRelayRequest("read_file_v2"); got != relayOutboundBulk {
		t.Fatalf("classifyRelayRequest(read_file_v2)=%v want relayOutboundBulk", got)
	}
	rc, out := newReadFileBulkTestConn(t, true, true)
	// 取回真实 macToIosKey：构造时传入的是 zero key copy，OpenEnvelope 需用同一 key。
	rc.mu.Lock()
	realKey := append([]byte(nil), rc.macToIosKey...)
	chunksOn := rc.outboundChunks
	rc.mu.Unlock()
	if !chunksOn {
		t.Fatal("test setup: outboundChunks 未启用")
	}

	rc.registerRequestClass("r1", "read_file_v2", "")
	// 高熵 100KB content → gzip 后仍 > relayChunkTargetBytes(32KiB) → 触发 gzip + chunk
	bigContent := highEntropyContent(100000)
	rc.SendResult("r1", map[string]any{"kind": "text", "content": bigContent}, nil)

	combined, sawChunk, encoding := collectResultFrames(t, out, realKey)
	if !sawChunk {
		t.Fatal("read_file_v2 大结果未分块")
	}
	if encoding != "gzip" {
		t.Errorf("contentEncoding=%q want gzip", encoding)
	}
	// combined 是 gzip 字节，解压后应为 result JSON
	gr, err := gzip.NewReader(bytes.NewReader(combined))
	if err != nil {
		t.Fatal(err)
	}
	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Type string `json:"type"`
		ID   string `json:"requestId"`
		OK   bool   `json:"ok"`
		Data struct {
			Kind    string `json:"kind"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(decompressed, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "result" || resp.ID != "r1" || !resp.OK {
		t.Errorf("reassembled result wrong: %+v", resp)
	}
	if resp.Data.Kind != "text" || len(resp.Data.Content) != 100000 {
		t.Errorf("content mismatch: kind=%q len=%d", resp.Data.Kind, len(resp.Data.Content))
	}
}

func TestReadFileV2SmallResultNotChunked(t *testing.T) {
	rc, out := newReadFileBulkTestConn(t, true, true)
	rc.mu.Lock()
	realKey := append([]byte(nil), rc.macToIosKey...)
	rc.mu.Unlock()

	rc.registerRequestClass("r2", "read_file_v2", "")
	// < relayChunkTargetBytes → 单帧，不 chunk（仍可能 gzip，但 100B < 32KiB 阈值也不 gzip）
	rc.SendResult("r2", map[string]any{"kind": "text", "content": "hello"}, nil)

	combined, sawChunk, _ := collectResultFrames(t, out, realKey)
	if sawChunk {
		t.Fatal("小结果不应分块")
	}
	var resp struct {
		Type string `json:"type"`
		ID   string `json:"requestId"`
	}
	if err := json.Unmarshal(combined, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "result" || resp.ID != "r2" {
		t.Errorf("small result wrong: %+v", resp)
	}
}

func TestReadFileV2NoChunksCapabilitySingleFrame(t *testing.T) {
	// 客户端未 ack relay_chunks_v1 → 即便结果很大，也不分块（base 兼容；§3.6.4 混合版本）。
	rc, out := newReadFileBulkTestConn(t, false, false)
	rc.mu.Lock()
	realKey := append([]byte(nil), rc.macToIosKey...)
	rc.mu.Unlock()

	rc.registerRequestClass("r3", "read_file_v2", "")
	rc.SendResult("r3", map[string]any{"kind": "text", "content": string(make([]byte, 100000))}, nil)

	combined, sawChunk, _ := collectResultFrames(t, out, realKey)
	if sawChunk {
		t.Fatal("未 ack chunks 的大结果不应分块")
	}
	var resp struct {
		Type string `json:"type"`
		ID   string `json:"requestId"`
	}
	if err := json.Unmarshal(combined, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "result" || resp.ID != "r3" {
		t.Errorf("single-frame result wrong: %+v", resp)
	}
}

// ── R1.4 correlation（§3.6.4）──────────────────────────────────────────────

const testCorrelation = "deadbeefdeadbeefdeadbeefdeadbeef" // 32 lowercase hex（合成测试值，非生产 token）

func TestBulkCorrelationRegistryLifecycle(t *testing.T) {
	r := NewBulkCorrelationRegistry(2, 2)
	// admit 两个不同 key
	if ok, reason := r.PutIfAbsent("a"); !ok || reason != "admitted" {
		t.Fatalf("a: ok=%v reason=%q", ok, reason)
	}
	if ok, _ := r.PutIfAbsent("b"); !ok {
		t.Fatal("b 应 admit")
	}
	// duplicate（active）
	if ok, reason := r.PutIfAbsent("a"); ok || reason != "already_active" {
		t.Fatalf("dup a: ok=%v reason=%q", ok, reason)
	}
	// active 满 → busy
	if ok, reason := r.PutIfAbsent("c"); ok || reason != "busy" {
		t.Fatalf("c: ok=%v reason=%q want busy", ok, reason)
	}
	// retire a → reuse 窗口
	if !r.Retire("a") {
		t.Fatal("retire a 失败")
	}
	if ok, reason := r.PutIfAbsent("a"); ok || reason != "reuse" {
		t.Fatalf("reuse a: ok=%v reason=%q want reuse", ok, reason)
	}
	// retire 不存在的 key → false
	if r.Retire("zzz") {
		t.Fatal("retire 未登记 key 应 false")
	}
}

// TestReadFileV2CorrelatedChunkStampsBulkCorrelationID：progress capability acked +
// read_file_v2 请求带 bulkCorrelationId → chunk 携带 correlation，且 correlation 绑入 AAD
// （缺它则解密失败）。
func TestReadFileV2CorrelatedChunkStampsBulkCorrelationID(t *testing.T) {
	rc, out := newReadFileBulkTestConn(t, true, false) // chunks on, gzip off（聚焦 correlation）
	rc.mu.Lock()
	realKey := append([]byte(nil), rc.macToIosKey...)
	rc.outboundChunkProgress = true // 模拟 client ack 了 relay_chunk_progress_v1
	rc.mu.Unlock()

	rc.registerRequestClass("c1", "read_file_v2", testCorrelation)
	rc.SendResult("c1", map[string]any{"kind": "text", "content": highEntropyContent(100000)}, nil)

	// 收集所有 chunk，逐个断言 correlation + AAD 绑定
	var chunks []RelayEnvelope
	deadline := time.After(2 * time.Second)
	for {
		select {
		case env := <-out:
			if env.Chunk == nil {
				t.Fatal("期望 chunked 帧，got single frame")
			}
			if env.Chunk.BulkCorrelationID != testCorrelation {
				t.Fatalf("chunk %d BulkCorrelationID=%q want %q", env.Chunk.Index, env.Chunk.BulkCorrelationID, testCorrelation)
			}
			chunks = append(chunks, env)
			if env.Chunk.Index+1 == env.Chunk.Count {
				goto done
			}
		case <-deadline:
			t.Fatal("超时等 chunk group")
		}
	}
done:
	if len(chunks) < 2 {
		t.Fatalf("应至少 2 个 chunk，got %d", len(chunks))
	}
	// AAD 绑定证明：用完整 AAD（含 correlation）解密成功；用剥离 correlation 的 AAD 解密失败。
	env := chunks[0]
	fullAAD, _ := env.EncodeAAD()
	if _, err := OpenEnvelope(realKey, env.Counter, fullAAD, env.Ciphertext); err != nil {
		t.Fatalf("完整 AAD 解密失败：%v", err)
	}
	// 构造 base AAD（chunk 不含 bulkCorrelationId），应解密失败
	baseEnv := env
	baseEnv.Chunk = &RelayChunkMetadata{GroupID: env.Chunk.GroupID, Index: env.Chunk.Index, Count: env.Chunk.Count}
	baseAAD, _ := baseEnv.EncodeAAD()
	if _, err := OpenEnvelope(realKey, env.Counter, baseAAD, env.Ciphertext); err == nil {
		t.Fatal("base AAD（无 correlation）竟解密成功：correlation 未绑入 AAD")
	}
	// correlation 已 retire（group 完成）
	if rc.bulkCorrelations.RetiredCount() != 1 || rc.bulkCorrelations.ActiveCount() != 0 {
		t.Errorf("retire 后 active=%d retired=%d，want 0/1", rc.bulkCorrelations.ActiveCount(), rc.bulkCorrelations.RetiredCount())
	}
}

// TestReadFileV2NoProgressCapabilityBaseChunk：未 ack progress → 即便请求带 correlation，
// 也不 stamp（base chunk，correlation 字段空，base AAD）。
func TestReadFileV2NoProgressCapabilityBaseChunk(t *testing.T) {
	rc, out := newReadFileBulkTestConn(t, true, false)
	// outboundChunkProgress 保持 false（默认）

	rc.registerRequestClass("c2", "read_file_v2", testCorrelation)
	rc.SendResult("c2", map[string]any{"kind": "text", "content": highEntropyContent(100000)}, nil)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case env := <-out:
			if env.Chunk == nil {
				t.Fatal("期望 chunked 帧")
			}
			if env.Chunk.BulkCorrelationID != "" {
				t.Fatalf("未 ack progress 不应 stamp correlation，got %q", env.Chunk.BulkCorrelationID)
			}
			if env.Chunk.Index+1 == env.Chunk.Count {
				return
			}
		case <-deadline:
			t.Fatal("超时")
		}
	}
}

// TestReadFileV2DuplicateCorrelationClosesGeneration：同一 correlation 已 active 时，
// 第二个 chunked result 的 PutIfAbsent 返回 already_active → close transport generation。
func TestReadFileV2DuplicateCorrelationClosesGeneration(t *testing.T) {
	rc, out := newReadFileBulkTestConn(t, true, false)
	rc.mu.Lock()
	rc.outboundChunkProgress = true
	rc.mu.Unlock()
	// 预登记 correlation 为 active（模拟另一个 in-flight request 已持有）
	if ok, _ := rc.bulkCorrelations.PutIfAbsent(testCorrelation); !ok {
		t.Fatal("预登记失败")
	}

	rc.registerRequestClass("c3", "read_file_v2", testCorrelation)
	rc.SendResult("c3", map[string]any{"kind": "text", "content": highEntropyContent(100000)}, nil)

	// 应 close，且不发出任何 chunk
	rc.mu.Lock()
	closed := rc.closed
	rc.mu.Unlock()
	if !closed {
		t.Fatal("duplicate correlation 应 close generation")
	}
	select {
	case env := <-out:
		t.Fatalf("duplicate correlation 不应发出帧，got %+v", env)
	case <-time.After(150 * time.Millisecond):
		// 无帧 = 通过
	}
}
