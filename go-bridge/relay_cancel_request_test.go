package gobridge

// R1.5（§3.6.4）cancel_request_v1 control RPC + committedToWriter too_late 边界测试。

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOutboundBulkHandleCancelCAS(t *testing.T) {
	// active → cancel 赢得 CAS → cancelled
	h := newOutboundBulkHandle("grp_x")
	if !h.Cancel() {
		t.Fatal("active handle Cancel 应返回 true（cancelled）")
	}
	if !h.Cancelled() {
		t.Fatal("Cancelled 应为 true")
	}
	if h.Index0Committed() {
		t.Fatal("不应 committed")
	}
	if !h.Cancel() {
		t.Fatal("重复 Cancel 应幂等 true")
	}

	// writer 先 commit index0 → cancel = too_late
	h2 := newOutboundBulkHandle("grp_y")
	if !h2.MarkIndex0Committed() {
		t.Fatal("active handle MarkIndex0Committed 应返回 true")
	}
	if !h2.Index0Committed() {
		t.Fatal("Index0Committed 应为 true")
	}
	if h2.Cancel() {
		t.Fatal("committed handle Cancel 应返回 false（too_late）")
	}

	// 先 cancel → MarkIndex0Committed 失败（cancel 已赢，writer 跳过）
	h3 := newOutboundBulkHandle("grp_z")
	h3.Cancel()
	if h3.MarkIndex0Committed() {
		t.Fatal("cancelled handle MarkIndex0Committed 应返回 false")
	}
}

// decryptSingleResult 从 out 取一帧（cancel result 是单帧非 chunk），解密返回 result map。
func decryptSingleResult(t *testing.T, out chan RelayEnvelope, key []byte) map[string]interface{} {
	t.Helper()
	combined, sawChunk, _ := collectResultFrames(t, out, key)
	if sawChunk {
		t.Fatal("cancel result 应为单帧")
	}
	var resp struct {
		Type     string                 `json:"type"`
		ID       string                 `json:"requestId"`
		OK       bool                   `json:"ok"`
		Data     map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(combined, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "result" || !resp.OK {
		t.Fatalf("want ok result, got %+v", resp)
	}
	return resp.Data
}

func TestCancelRequestV1_RelayOutcomes(t *testing.T) {
	h := newTestHandlers(t)
	rc, out := newReadFileBulkTestConn(t, true, false)
	rc.mu.Lock()
	key := append([]byte(nil), rc.macToIosKey...)
	rc.mu.Unlock()

	// 1. not_found：未知 requestId
	params, _ := json.Marshal(map[string]interface{}{"requestId": "nope"})
	h.handleCancelRequest(rc, WireMessage{RequestID: "c1", Params: params})
	if data := decryptSingleResult(t, out, key); data["outcome"] != "not_found" {
		t.Errorf("unknown request: outcome=%v want not_found", data["outcome"])
	}

	// 2. cancelled：预装 active handle（未 commit index0）
	rc.installRequestBulkHandle("rf-1", newOutboundBulkHandle("grp_a"))
	params, _ = json.Marshal(map[string]interface{}{"requestId": "rf-1"})
	h.handleCancelRequest(rc, WireMessage{RequestID: "c2", Params: params})
	if data := decryptSingleResult(t, out, key); data["outcome"] != "cancelled" {
		t.Errorf("active handle: outcome=%v want cancelled", data["outcome"])
	}

	// 3. too_late：预装已 commit 的 handle
	committed := newOutboundBulkHandle("grp_b")
	committed.MarkIndex0Committed()
	rc.installRequestBulkHandle("rf-2", committed)
	params, _ = json.Marshal(map[string]interface{}{"requestId": "rf-2"})
	h.handleCancelRequest(rc, WireMessage{RequestID: "c3", Params: params})
	if data := decryptSingleResult(t, out, key); data["outcome"] != "too_late" {
		t.Errorf("committed handle: outcome=%v want too_late", data["outcome"])
	}
}

func TestCancelRequestV1_MissingParam(t *testing.T) {
	h := newTestHandlers(t)
	rc, out := newReadFileBulkTestConn(t, true, false)
	rc.mu.Lock()
	key := append([]byte(nil), rc.macToIosKey...)
	rc.mu.Unlock()

	h.handleCancelRequest(rc, WireMessage{RequestID: "c4", Params: nil})
	// 期望 error result（非 ok）
	env := <-out
	aad, _ := env.EncodeAAD()
	plain, err := OpenEnvelope(key, env.Counter, aad, env.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Type string `json:"type"`
		OK   bool   `json:"ok"`
	}
	if err := json.Unmarshal(plain, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Error("空 requestId 应返回 error result（ok=false）")
	}
}

func TestCancelRequestV1_DirectConnTooLate(t *testing.T) {
	h := newTestHandlers(t)
	// 非 RelayDeviceConn → too_late（Direct 单帧已 commit；R1.9 细化 Direct 边界）
	conn := newReadFileCaptureConn()
	params, _ := json.Marshal(map[string]interface{}{"requestId": "rf-x"})
	h.handleCancelRequest(conn, WireMessage{RequestID: "c5", Params: params})
	if conn.err != nil {
		t.Fatalf("unexpected err: %+v", conn.err)
	}
	res, ok := conn.data.(map[string]interface{})
	if !ok || res["outcome"] != "too_late" {
		t.Errorf("direct: data=%#v want outcome=too_late", conn.data)
	}
}

// TestWriteSelectedSkipsCancelledGroup：handle 被 cancel 后，writer.writeSelected 在 index0
// 前 CAS 失败 → 返回 (nil, true) superseded，不写出任何 chunk（R1.5 用户可见行为）。
func TestWriteSelectedSkipsCancelledGroup(t *testing.T) {
	rc, out := newReadFileBulkTestConn(t, true, false)
	w := newRelayOutboundWriter()
	t.Cleanup(w.close)

	handle := newOutboundBulkHandle("grp_t")
	handle.Cancel() // 预取消（模拟 cancel_request_v1 在 index0 前命中）
	job := &relayOutboundJob{
		conn:    rc,
		payload: []byte(highEntropyContent(100000)),
		cursor: &relayChunkCursor{
			groupID: "grp_t", nextIndex: 0, count: 3, chunkBytes: 32 << 10, handle: handle,
			expiresAt: time.Now().Add(time.Minute),
		},
	}
	err, complete := w.writeSelected(job)
	if err != nil {
		t.Fatalf("writeSelected err=%v（应 nil）", err)
	}
	if !complete {
		t.Fatal("cancelled group 应 complete=true（superseded，不再 requeue）")
	}
	// 不应写出任何 chunk
	select {
	case env := <-out:
		t.Fatalf("cancelled group 不应写出 chunk，got %+v", env)
	case <-time.After(50 * time.Millisecond):
		// 无帧 = 通过
	}
}
