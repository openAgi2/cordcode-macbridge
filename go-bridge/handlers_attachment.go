package gobridge

// handlers_attachment.go — S4 附件读取（OD-3）：官方 session/attachment 的桥接
// RPC。attachmentId 来自 user_message 附件描述符（journal image 块，
// "sha256:<hex>"）；官方读取侧自带 referencedImage journal 证明——未引用 id
// 返回 session/attachment-invalid ATTACHMENT_NOT_REFERENCED（A4a 活体负例
// 原文），verbatim 透传不吞不改（坑 7）。图片字节不进时间线事件——客户端按
// 需经本 RPC 懒取。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleGetAttachment(conn Connection, msg WireMessage, agent core.Agent) {
	reader, ok := agent.(core.AttachmentReader)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support attachment reads"})
		return
	}
	var params GetAttachmentParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "sessionId required"})
		return
	}
	if strings.TrimSpace(params.AttachmentID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "attachmentId required"})
		return
	}
	// Attachment bytes can be large; keep a bounded but generous window
	// (official admission caps batch sizes, single images stay well below).
	ctx, cancel := context.WithTimeout(h.ctx, 60*time.Second)
	defer cancel()
	data, err := reader.ReadAttachment(ctx, params.SessionID, params.AttachmentID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "attachment_read_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]any{
		"attachment": data.Ref,
		"data":       base64.StdEncoding.EncodeToString(data.Data),
	}, nil)
}
