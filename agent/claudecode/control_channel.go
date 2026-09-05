package claudecode

import (
	"context"
	"fmt"
	"time"
)

// control_channel.go 实现官方控制协议的发送侧（bridge → CLI，stdin）：
//
//	{"type":"control_request","request_id":<id>,"request":{"subtype":<subtype>,…}}
//
// 配对收件走 stdout 的 control_response 帧（Phase 0 证据包 2026-09-04，CLI 2.1.234）：
//
//	{"type":"control_response","response":{"subtype":"success","request_id":<id>,"response":{…}}}
//
// 注意成功体载荷嵌套在第二层 response 字段；subtype 嵌套在 response 内（设计 §3.1，
// sdk.d.ts:4285/4332）。本文件只做信封与配对，不做 subtype 语义——调用方按需解析，
// 未知形状必须 fail closed。
//
// Phase 1 使用者：initialize（目录主源）/ list_models（刷新）。
// Phase 2 在同一通道上扩展 set_model / set_permission_mode / interrupt。

// ctrlWriteTimeout bounds a single stdin write; the per-request wait uses the
// caller's context (see sendControlRequest).
const ctrlWriteTimeout = 5 * time.Second

// ctrlDefaultTimeout is the default wait for a control_response when the
// caller passes a context without a deadline.
const ctrlDefaultTimeout = 15 * time.Second

// controlResponse is the decoded response envelope inner object:
// {"subtype":"success"|"error","request_id":…,…payload}.
type controlResponse struct {
	Subtype   string         `json:"subtype"`
	RequestID string         `json:"request_id"`
	Raw       map[string]any `json:"-"`
}

// sendControlRequest writes one control_request envelope and waits for the
// control_response echoing the same request_id. Foreign request_ids are
// ignored by dispatch. Returns the decoded response; callers inspect Subtype
// ("success" / "error") and fail closed on anything they don't understand.
func (cs *claudeSession) sendControlRequest(ctx context.Context, inner map[string]any) (controlResponse, error) {
	rid := fmt.Sprintf("cc-ctrl-%d", cs.ctrlReqSeq.Add(1))
	ch := make(chan controlResponse, 1)

	cs.ctrlMu.Lock()
	if cs.ctrlPending == nil {
		cs.ctrlPending = make(map[string]chan controlResponse)
	}
	cs.ctrlPending[rid] = ch
	cs.ctrlMu.Unlock()
	defer func() {
		cs.ctrlMu.Lock()
		delete(cs.ctrlPending, rid)
		cs.ctrlMu.Unlock()
	}()

	envelope := map[string]any{
		"type":       "control_request",
		"request_id": rid,
		"request":    inner,
	}

	writeCtx, cancel := context.WithTimeout(ctx, ctrlWriteTimeout)
	defer cancel()
	if err := cs.writeJSONContext(writeCtx, envelope); err != nil {
		return controlResponse{}, fmt.Errorf("control write: %w", err)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ctrlDefaultTimeout)
		defer cancel()
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return controlResponse{}, fmt.Errorf("control response timeout: %w", ctx.Err())
	case <-cs.done:
		return controlResponse{}, fmt.Errorf("session closed before control response")
	}
}

// dispatchControlResponse routes one stdout control_response frame to its
// pending request channel. Returns false when the frame is malformed or
// matches no pending request (logged, never fatal — a late response for an
// abandoned request_id is legal per the SDK contract).
func (cs *claudeSession) dispatchControlResponse(raw map[string]any) bool {
	respObj, ok := raw["response"].(map[string]any)
	if !ok {
		return false
	}
	rid, _ := respObj["request_id"].(string)
	if rid == "" {
		return false
	}
	cs.ctrlMu.Lock()
	ch, ok := cs.ctrlPending[rid]
	cs.ctrlMu.Unlock()
	if !ok {
		return false
	}
	subtype, _ := respObj["subtype"].(string)
	// The success payload nests under a second "response" key; deliver the
	// whole object so callers keep the raw shape (fail-closed parsing is
	// theirs).
	cr := controlResponse{Subtype: subtype, RequestID: rid, Raw: respObj}
	select {
	case ch <- cr:
	default: // waiter already gone (timeout/cancel); drop
	}
	return true
}
