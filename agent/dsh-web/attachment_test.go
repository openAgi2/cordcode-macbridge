package dshweb

// attachment_test.go — S4 图片附件 + 文件 receipts（OD-3=B）wire 契约：
// 形状全部钉死自 A4a/A4b 生产座位活体证据
// (scripts/dshweb-phase0/alpha1-attachment-wire.json /
// alpha1-file-receipts-wire.json)：
//   - 发送：prompt content part {type:'image',mediaType,data,name} +
//     {type:'file',receiptId}；文件先走原始字节路由
//     POST /api/session/uploadFileBinary?sessionId=&name=
//     (application/octet-stream) → {ok:true,value:{receiptId,file}}；
//   - 接收：journal user/message 的 image/file 块（attachment:
//     {attachmentId:'sha256:…',…}）→ 附件描述符；纯附件行冷拉不再被丢；
//   - 读取：session/attachment {request:{sessionId,attachmentId}} →
//     {attachment,data:base64}，错误 verbatim。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSendAttachmentContentParts(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-att")
	f.handlers["session/prompt"] = fakeRPCResponse{value: map[string]any{"accepted": true}}
	f.upload = uploadScript{receipt: "receipt-1"}

	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	err := sess.Send("look at this",
		[]core.ImageAttachment{{MimeType: "image/png", Data: png, FileName: "/Users/x/Pictures/local shot.png"}},
		[]core.FileAttachment{{MimeType: "text/plain", Data: []byte("file body"), FileName: "notes.txt"}})
	if err != nil {
		t.Fatal(err)
	}

	// The raw upload ran first, with the exact official route contract.
	f.uploads.mu.Lock()
	ups := append([]recordedUpload(nil), f.uploads.list...)
	f.uploads.mu.Unlock()
	if len(ups) != 1 {
		t.Fatalf("raw uploads = %d, want 1", len(ups))
	}
	if string(ups[0].data) != "file body" || ups[0].sessionID != "sess-att" || ups[0].name != "notes.txt" {
		t.Fatalf("upload = %+v, want body/file body on sess-att as notes.txt", ups[0])
	}

	calls := methodCalls(f, "session/prompt")
	if len(calls) != 1 {
		t.Fatalf("session/prompt calls = %d, want 1", len(calls))
	}
	var payload struct {
		Args struct {
			Request struct {
				Mode    string `json:"mode"`
				Content []struct {
					Type      string `json:"type"`
					Text      string `json:"text"`
					MediaType string `json:"mediaType"`
					Data      string `json:"data"`
					Name      string `json:"name"`
					ReceiptID string `json:"receiptId"`
				} `json:"content"`
			} `json:"request"`
		} `json:"args"`
	}
	if err := json.Unmarshal(calls[0], &payload); err != nil {
		t.Fatal(err)
	}
	parts := payload.Args.Request.Content
	if len(parts) != 3 {
		t.Fatalf("content parts = %d, want 3 (text, image, file)", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "look at this" {
		t.Fatalf("text part = %+v", parts[0])
	}
	if parts[1].Type != "image" || parts[1].MediaType != "image/png" {
		t.Fatalf("image part = %+v", parts[1])
	}
	if got, err := base64.StdEncoding.DecodeString(parts[1].Data); err != nil || string(got) != string(png) {
		t.Fatalf("image data must be canonical base64 of the bytes: %q", parts[1].Data)
	}
	// Local path never reaches the wire — only the official leaf name.
	if parts[1].Name != "local shot.png" {
		t.Fatalf("image name = %q, want leaf only", parts[1].Name)
	}
	if parts[2].Type != "file" || parts[2].ReceiptID != "receipt-1" {
		t.Fatalf("file part = %+v, want the staged receipt", parts[2])
	}
}

func TestSendUploadFailureAbortsPrompt(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-att-err")
	f.upload = uploadScript{err: &RPCError{Code: "session/attachment-invalid",
		Message: "File upload is not canonical base64.", Details: json.RawMessage(`{"reason":"INVALID_FILE_BASE64"}`)}}

	err := sess.Send("with file", nil,
		[]core.FileAttachment{{MimeType: "text/plain", Data: []byte("x"), FileName: "a.txt"}})
	if err == nil || !strings.Contains(err.Error(), "session/attachment-invalid") ||
		!strings.Contains(err.Error(), "File upload is not canonical base64.") {
		t.Fatalf("upload failure must surface the official error verbatim, got %v", err)
	}
	if calls := methodCalls(f, "session/prompt"); len(calls) != 0 {
		t.Fatalf("prompt must not be sent after an upload failure, saw %d calls", len(calls))
	}
}

func TestReadAttachmentWire(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	png := []byte{0x89, 'P', 'N', 'G'}
	f.handlers["session/attachment"] = fakeRPCResponse{value: map[string]any{
		"attachment": map[string]any{
			"attachmentId": "sha256:06f0c5e9", "mediaType": "image/png",
			"bytes": len(png), "width": 8, "height": 8, "name": "probe-red.png",
		},
		"data": base64.StdEncoding.EncodeToString(png),
	}}

	data, err := a.ReadAttachment(context.Background(), "sess-att", "sha256:06f0c5e9")
	if err != nil {
		t.Fatal(err)
	}
	if string(data.Data) != string(png) {
		t.Fatalf("decoded bytes = %v", data.Data)
	}
	ref := data.Ref
	if ref.Kind != "image" || ref.AttachmentID != "sha256:06f0c5e9" || ref.MediaType != "image/png" ||
		ref.Name != "probe-red.png" || ref.Width != 8 || ref.Height != 8 {
		t.Fatalf("ref = %+v", ref)
	}
	calls := methodCalls(f, "session/attachment")
	if len(calls) != 1 {
		t.Fatalf("session/attachment calls = %d, want 1", len(calls))
	}
	var payload struct {
		Args struct {
			Request struct {
				SessionID    string `json:"sessionId"`
				AttachmentID string `json:"attachmentId"`
			} `json:"request"`
		} `json:"args"`
	}
	if err := json.Unmarshal(calls[0], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Args.Request.SessionID != "sess-att" || payload.Args.Request.AttachmentID != "sha256:06f0c5e9" {
		t.Fatalf("args = %+v", payload.Args.Request)
	}

	// Official negative verbatim: ATTACHMENT_NOT_REFERENCED (A4a evidence).
	f.handlers["session/attachment"] = fakeRPCResponse{err: &RPCError{
		Code: "session/attachment-invalid", Message: "Image is not referenced by this session.",
		Details: json.RawMessage(`{"reason":"ATTACHMENT_NOT_REFERENCED"}`)}}
	if _, err := a.ReadAttachment(context.Background(), "sess-att", "sha256:bogus"); err == nil ||
		!strings.Contains(err.Error(), "session/attachment-invalid") ||
		!strings.Contains(err.Error(), "Image is not referenced by this session.") {
		t.Fatalf("foreign attachment must surface the official error verbatim, got %v", err)
	}
}

// A4a/A4b journal shapes (verbatim from the live evidence): the admitted
// user/message carries top-level image/file blocks with the durable ref.
func TestCodecUserMessageAttachmentBlocks(t *testing.T) {
	c := newSessionCodec("sess-att")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		env("user/message", 1, map[string]any{
			"id": "m-att-1",
			"content": []map[string]any{
				{"type": "text", "text": "describe these"},
				{"type": "image", "attachment": map[string]any{
					"attachmentId": "sha256:06f0c5e9", "mediaType": "image/png",
					"bytes": 74, "width": 8, "height": 8, "name": "probe-red.png"}},
				{"type": "file", "attachment": map[string]any{
					"attachmentId": "sha256:1944ed8f", "name": "receipt-notes.txt", "bytes": 55}},
			},
			"source": map[string]any{"kind": "user"},
		}),
		env("turn/end", 2, map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}),
	})
	var user *core.Event
	for i := range events {
		if events[i].Type == core.EventUserMessage {
			user = &events[i]
		}
	}
	if user == nil {
		t.Fatal("user message expected")
	}
	if len(user.Attachments) != 2 {
		t.Fatalf("attachments = %+v, want image+file", user.Attachments)
	}
	img, file := user.Attachments[0], user.Attachments[1]
	if img.Kind != "image" || img.AttachmentID != "sha256:06f0c5e9" || img.MediaType != "image/png" ||
		img.Name != "probe-red.png" || img.Bytes != 74 || img.Width != 8 || img.Height != 8 {
		t.Fatalf("image descriptor = %+v", img)
	}
	if file.Kind != "file" || file.AttachmentID != "sha256:1944ed8f" || file.Name != "receipt-notes.txt" || file.Bytes != 55 {
		t.Fatalf("file descriptor = %+v", file)
	}
}

func TestHistoryUserMessageAttachments(t *testing.T) {
	evs := []sessionEventWire{
		mkHistoryEntry("turn/start", 100, `{"turn": 1}`),
		mkHistoryEntry("user/message", 101, `{
			"content": [
				{"type": "image", "attachment": {"attachmentId": "sha256:06f0c5e9", "mediaType": "image/png", "bytes": 74, "width": 8, "height": 8, "name": "probe-red.png"}},
				{"type": "file", "attachment": {"attachmentId": "sha256:1944ed8f", "name": "receipt-notes.txt", "bytes": 55}}
			],
			"source": {"kind": "user"}
		}`),
		mkHistoryEntry("turn/end", 200, `{"turn": 1, "reason": {"kind": "completed"}}`),
	}
	entries := mapHistoryEvents("sess-att-hist", evs)
	var e *core.RichHistoryEntry
	for i := range entries {
		if entries[i].Role == "user" {
			e = &entries[i]
			break
		}
	}
	if e == nil {
		t.Fatal("the attachment-only user row must survive cold history")
	}
	if e.Content != "" {
		t.Fatalf("entry content = %q, want text-less user row", e.Content)
	}
	if len(e.Attachments) != 2 || e.Attachments[0].Kind != "image" || e.Attachments[1].Kind != "file" {
		t.Fatalf("attachments = %+v", e.Attachments)
	}
}
