package gobridge

// projection_attachment_test.go — S4 附件（OD-3）的 reducer/wire/capability
// 侧：user_message 的附件描述符（journal image/file 块，A4a/A4b 活体形状）
// 随帧进 TurnProjection.User.Attachments（占位与落定同规则，present-wins）；
// mapAgentEvent 把 core.EventAttachment 原样上 wire；attachment_read
// capability 只随 AttachmentReader 广告。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
)

func testAttachments() []core.EventAttachment {
	return []core.EventAttachment{
		{Kind: "image", AttachmentID: "sha256:06f0c5e9", MediaType: "image/png",
			Name: "probe-red.png", Bytes: 74, Width: 8, Height: 8},
		{Kind: "file", AttachmentID: "sha256:1944ed8f", Name: "receipt-notes.txt", Bytes: 55},
	}
}

func TestReducerUserMessageAttachmentsRideProjection(t *testing.T) {
	r := newTestReducer()
	// 排队占位带附件：pending 行的 user 投影立即携带描述符。
	r.Apply(ev(1, "dsh-web", "s1", "user_message", map[string]interface{}{
		"itemId": "m-att", "text": "queued with attachments", "pending": true,
		"attachments": testAttachments(),
	}))
	proj, _ := r.Snapshot("dsh-web", "s1")
	if len(proj.Turns) != 1 || proj.Turns[0].User == nil {
		t.Fatalf("placeholder row missing: %+v", proj.Turns)
	}
	if len(proj.Turns[0].User.Attachments) != 2 ||
		proj.Turns[0].User.Attachments[0].AttachmentID != "sha256:06f0c5e9" ||
		proj.Turns[0].User.Attachments[1].Kind != "file" {
		t.Fatalf("placeholder attachments = %+v", proj.Turns[0].User.Attachments)
	}

	// 落定（同 id，真实回合）：权威 journal 行替换占位，附件随帧重申。
	r.Apply(ev(2, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T9"}))
	r.Apply(ev(3, "dsh-web", "s1", "user_message", map[string]interface{}{
		"itemId": "m-att", "turnId": "T9", "text": "queued with attachments",
		"attachments": testAttachments(),
	}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	var settled *TurnProjection
	for i := range proj.Turns {
		if proj.Turns[i].TurnID == "T9" {
			settled = &proj.Turns[i]
		}
	}
	if settled == nil || settled.User == nil {
		t.Fatalf("settled turn missing: %+v", proj.Turns)
	}
	if len(settled.User.Attachments) != 2 || settled.User.Attachments[0].Width != 8 {
		t.Fatalf("settled attachments = %+v", settled.User.Attachments)
	}
}

func TestMapAgentEventUserMessageAttachments(t *testing.T) {
	ev := core.Event{
		Type:    core.EventUserMessage,
		Content: "with attachments",
		TurnID:  "T9",
		ItemID:  "m-att",
		Attachments: testAttachments(),
	}
	name, data, _ := mapAgentEvent(ev)
	if name != "user_message" {
		t.Fatalf("event = %q", name)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Attachments []core.EventAttachment `json:"attachments"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	if len(frame.Attachments) != 2 || frame.Attachments[0].AttachmentID != "sha256:06f0c5e9" {
		t.Fatalf("wire attachments = %+v", frame.Attachments)
	}

	// 无附件的帧不携带该键（additive，旧快照不受影响）。
	_, data2, _ := mapAgentEvent(core.Event{Type: core.EventUserMessage, Content: "plain",
		TurnID: "T9", ItemID: "m2"})
	if m, ok := data2.(map[string]interface{}); ok {
		if _, has := m["attachments"]; has {
			t.Fatal("attachments key must be absent when the message carries none")
		}
	}
}

func TestAttachmentReadCapabilityAdvertisement(t *testing.T) {
	found := false
	for _, c := range deriveBackendCapabilities("dsh-web", &attachmentReaderAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}, "") {
		if c == "attachment_read" {
			found = true
		}
	}
	if !found {
		t.Fatal("dsh-web (AttachmentReader) must advertise attachment_read")
	}
	for _, id := range []string{"grok", "claude", "codex-remote", "opencode-web"} {
		for _, c := range deriveBackendCapabilities(id, &fakeAgent{name: id}, "") {
			if c == "attachment_read" {
				t.Fatalf("%s must NOT advertise attachment_read (no AttachmentReader)", id)
			}
		}
	}
}

type attachmentReaderAgent struct {
	*fakeAgent
}

func (a *attachmentReaderAgent) ReadAttachment(_ context.Context, _, _ string) (*core.AttachmentData, error) {
	return nil, nil
}

func TestHandleGetAttachment(t *testing.T) {
	h := NewHandlersWithContext(context.Background())
	png := []byte{0x89, 'P', 'N', 'G'}

	// 非 AttachmentReader backend → not_supported。
	conn := newCaptureConn()
	h.handleGetAttachment(conn, WireMessage{RequestID: "r1",
		Params: mustJSON(t, map[string]string{"sessionId": "s1", "attachmentId": "sha256:x"})},
		&fakeAgent{name: "grok"})
	if conn.lastErrCode != "not_supported" {
		t.Fatalf("non-reader must get not_supported, got %q", conn.lastErrCode)
	}

	// 成功：官方 ref verbatim + base64 字节。
	reader := &scriptedReaderAgent{fakeAgent: &fakeAgent{name: "dsh-web"},
		data: &core.AttachmentData{
			Ref:  core.EventAttachment{Kind: "image", AttachmentID: "sha256:06f0c5e9", MediaType: "image/png", Name: "probe-red.png", Bytes: int64(len(png)), Width: 8, Height: 8},
			Data: png,
		}}
	conn2 := newCaptureConn()
	h.handleGetAttachment(conn2, WireMessage{RequestID: "r2",
		Params: mustJSON(t, map[string]string{"sessionId": "s1", "attachmentId": "sha256:06f0c5e9"})}, reader)
	if conn2.lastErrCode != "" {
		t.Fatalf("read failed: %q (%s)", conn2.lastErrCode, conn2.lastErr.Message)
	}
	var value struct {
		Attachment core.EventAttachment `json:"attachment"`
		Data       string              `json:"data"`
	}
	if err := json.Unmarshal(conn2.lastResultJSON, &value); err != nil {
		t.Fatal(err)
	}
	if value.Attachment.AttachmentID != "sha256:06f0c5e9" || value.Attachment.MediaType != "image/png" ||
		value.Attachment.Width != 8 || value.Attachment.Height != 8 {
		t.Fatalf("attachment ref = %+v", value.Attachment)
	}
	if got, err := base64.StdEncoding.DecodeString(value.Data); err != nil || string(got) != string(png) {
		t.Fatalf("data must be canonical base64 of the bytes, got %q", value.Data)
	}

	// 官方错误 verbatim：attachment_read_failed 携带原文。
	failing := &failingReaderAgent{fakeAgent: &fakeAgent{name: "dsh-web"},
		err: errString("dsh rpc error session/attachment-invalid: Image is not referenced by this session.")}
	conn3 := newCaptureConn()
	h.handleGetAttachment(conn3, WireMessage{RequestID: "r3",
		Params: mustJSON(t, map[string]string{"sessionId": "s1", "attachmentId": "sha256:bogus"})}, failing)
	if conn3.lastErrCode != "attachment_read_failed" ||
		!strings.Contains(conn3.lastErr.Message, "Image is not referenced by this session.") {
		t.Fatalf("foreign id must surface the official error verbatim, got %q %q",
			conn3.lastErrCode, conn3.lastErr.Message)
	}
}

type scriptedReaderAgent struct {
	*fakeAgent
	data *core.AttachmentData
}

func (a *scriptedReaderAgent) ReadAttachment(_ context.Context, _, _ string) (*core.AttachmentData, error) {
	return a.data, nil
}

type failingReaderAgent struct {
	*fakeAgent
	err error
}

func (a *failingReaderAgent) ReadAttachment(_ context.Context, _, _ string) (*core.AttachmentData, error) {
	return nil, a.err
}

type errString string

func (e errString) Error() string { return string(e) }

func TestSendPromptModeFailsVisiblyWithoutOptionsSender(t *testing.T) {
	// OD-2a=A: steer on a backend without per-request modes must fail
	// visibly instead of silently degrading to queue.
	plain := &plainTestSession{}
	if err := sendPrompt(plain, "hi", nil, nil, core.PromptOptions{Mode: "steer"}); err == nil {
		t.Fatal("steer on a non-options backend must fail visibly")
	}
	if err := sendPrompt(plain, "hi", nil, nil, core.PromptOptions{Mode: "queue"}); err != nil {
		t.Fatalf("explicit queue must pass through: %v", err)
	}
	if err := sendPrompt(plain, "hi", nil, nil, core.PromptOptions{}); err != nil {
		t.Fatalf("empty mode must pass through: %v", err)
	}
}

// plainTestSession is the plain AgentSession surface (no PromptOptionsSender).
type plainTestSession struct{}

func (s *plainTestSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	return nil
}
func (s *plainTestSession) RespondPermission(requestID string, result core.PermissionResult) error {
	return nil
}
func (s *plainTestSession) RespondQuestion(questionID string, optionIDs []string) error { return nil }
func (s *plainTestSession) RejectQuestion(requestID string) error                          { return nil }
func (s *plainTestSession) CurrentSessionID() string                                      { return "" }
func (s *plainTestSession) Events() <-chan core.Event                                     { return nil }
func (s *plainTestSession) Alive() bool                                                   { return true }
func (s *plainTestSession) Close() error                                                  { return nil }

func TestAdmitBridgeSteerBypassesTurnSlot(t *testing.T) {
	// OD-2a=A: a steer splices into the RUNNING turn — the per-session
	// turn-creating slot must not block it (the running turn already holds
	// the slot), while the quiesce drain still rejects.
	h := NewHandlersWithContext(context.Background())
	// No admission machine (dev mode): both admit.
	if wireErr := h.admitBridgeSteer(); wireErr != nil {
		t.Fatalf("steer without admission machine must admit, got %+v", wireErr)
	}
	// Install a real machine so the turn slot is enforced (bridge_turn_slot_
	// leak test pattern).
	h.SetAdmissionMachine(admission.NewAdmissionMachine(admission.RuntimeIdentity{PID: 1, BridgeEpoch: 1}, nil, 30_000))
	// A held turn slot does not block steer (only admitBridgeTurn checks it).
	h.mu.Lock()
	h.bridgeOwnedTurns["sess-x"] = struct{}{}
	h.mu.Unlock()
	if wireErr := h.admitBridgeSteer(); wireErr != nil {
		t.Fatalf("steer must bypass the per-session turn slot, got %+v", wireErr)
	}
	if wireErr := h.admitBridgeTurn("sess-x"); wireErr == nil || wireErr.Code != "session_action_in_progress" {
		t.Fatalf("turn-creating send must still be serialized, got %+v", wireErr)
	}
	h.mu.Lock()
	delete(h.bridgeOwnedTurns, "sess-x")
	h.mu.Unlock()
}
