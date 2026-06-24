package gobridge

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// captureSlogText 将 slog 默认 handler 重定向到 buffer，返回恢复函数。
func captureSlogText(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	return buf, func() { slog.SetDefault(prev) }
}

// TestHandleSendMessageDoesNotLogContent 验证 P1-5：默认日志不含用户消息正文。
func TestHandleSendMessageDoesNotLogContent(t *testing.T) {
	handlers := NewHandlers()
	agent := &fakeAgent{name: "codex"}
	handlers.RegisterAgent("codex", agent)
	session := &fakeAgentSession{id: "ses_redact", events: make(chan core.Event, 8)}
	handlers.putSessionWithMeta("ses_redact", "codex", "/tmp/p", session)

	secretContent := "SUPER_SECRET_API_KEY_sk-1234567890 very long user prompt with source code"
	conn := &readFileCaptureConn{}
	msg := WireMessage{
		Type:      "request",
		RequestID: "req_redact",
		BackendID: "codex",
		Method:    "send_message",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_redact", "content": secretContent}),
	}

	buf, restore := captureSlogText(t)
	defer restore()

	handlers.HandleRPC(conn, msg)

	out := buf.String()
	if strings.Contains(out, "SUPER_SECRET_API_KEY") {
		t.Fatalf("日志泄露了用户消息正文:\n%s", out)
	}
	if strings.Contains(out, secretContent[:20]) {
		t.Fatalf("日志泄露了消息预览:\n%s", out)
	}
	// 仍应记录长度（非敏感元数据）。
	if !strings.Contains(out, "contentLen") {
		t.Fatalf("应记录 contentLen 元数据:\n%s", out)
	}
}

// TestDeviceManagementTokensNotLogged 验证 token/credential 不进默认日志。
// 通过 ValidateDeviceAuth 路径不记录明文 token（仅记录错误码）。
func TestDeviceManagementTokensNotLogged(t *testing.T) {
	store := newTestStore()
	plain, hash, _ := GenerateDeviceToken()
	rec := makeTestRecord("dev1")
	rec.TokenHash = hash
	_ = store.AddDevice(rec)

	buf, restore := captureSlogText(t)
	defer restore()

	// 错误 token 不应把明文 token 写入日志。
	_, _ = ValidateDeviceAuth(store, plain, "wrong-device-id")
	out := buf.String()
	if strings.Contains(out, plain) {
		t.Fatalf("默认日志泄露了 device token 明文:\n%s", out)
	}
}
