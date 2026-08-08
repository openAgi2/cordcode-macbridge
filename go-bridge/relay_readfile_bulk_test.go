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

	rc.registerRequestClass("r1", "read_file_v2")
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

	rc.registerRequestClass("r2", "read_file_v2")
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

	rc.registerRequestClass("r3", "read_file_v2")
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
