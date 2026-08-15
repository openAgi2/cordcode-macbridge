package gobridge

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// AttachmentInput 对应 unified-bridge-protocol 的 send_message.attachments[]。
// kind="image"（或 mime 为 image/*）走图片路径，其余走文件路径；base64 为原始字节的标准 base64。
type AttachmentInput struct {
	Kind     string `json:"kind"`               // "image" | "file"
	Mime     string `json:"mime"`               // e.g. "image/png"
	Filename string `json:"filename,omitempty"` // 原始文件名（可选）
	Base64   string `json:"base64,omitempty"`   // 标准 base64 编码字节
}

// mimeSyntax matches `type/subtype` after trim+lowercase. Parameters (e.g.
// "; charset=utf-8") are NOT accepted — canonical AttachmentInput.mime is a
// bare MIME type (design §3.9 single-path a).
var mimeSyntax = regexp.MustCompile(`^[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+$`)

// classifyAttachment is the ONE classification rule shared by the pre-check
// support gate and splitAttachments (design §3.9 / round11 P0-1): a raw kind
// of "image" OR a normalized image/* MIME prefix classifies as image;
// everything else is file. Pre-check and split must never carry two separate
// judgments — {kind:"file", mime:"image/png"} used to slip past a file-only
// gate and get reclassified as an image downstream.
func classifyAttachment(a AttachmentInput) string {
	if a.Kind == "image" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Mime)), "image/") {
		return "image"
	}
	return "file"
}

// validateAttachmentStructure is the raw-structure pass (§3.9 path a) applied
// to EVERY attachment: kind vocabulary, non-empty syntactically valid MIME,
// and base64 that decodes to non-empty bytes. Any failure rejects the WHOLE
// message (invalid_params) — no partial processing, no silent drops.
func validateAttachmentStructure(inputs []AttachmentInput) error {
	for i, a := range inputs {
		if a.Kind != "image" && a.Kind != "file" {
			return fmt.Errorf("attachments[%d]: kind must be \"image\" or \"file\", got %q", i, a.Kind)
		}
		mime := strings.ToLower(strings.TrimSpace(a.Mime))
		if mime == "" {
			return fmt.Errorf("attachments[%d]: mime is required", i)
		}
		if !mimeSyntax.MatchString(mime) {
			return fmt.Errorf("attachments[%d]: malformed mime %q (want bare type/subtype, no parameters)", i, a.Mime)
		}
		if a.Base64 == "" {
			return fmt.Errorf("attachments[%d]: base64 payload is required", i)
		}
		data, err := base64.StdEncoding.DecodeString(a.Base64)
		if err != nil {
			return fmt.Errorf("attachments[%d]: invalid base64: %v", i, err)
		}
		if len(data) == 0 {
			return fmt.Errorf("attachments[%d]: base64 payload decodes to empty bytes", i)
		}
	}
	return nil
}

// attachmentSupport returns the backend's positively declared attachment
// kinds (core.AttachmentSupporter). Absence of the interface or of a kind
// means NOT supported — never a negative inference the other way.
func attachmentSupport(agent core.Agent) map[string]bool {
	supported := make(map[string]bool)
	if sup, ok := agent.(core.AttachmentSupporter); ok {
		for _, kind := range sup.SupportedAttachmentKinds() {
			supported[kind] = true
		}
	}
	return supported
}

// validateSendMessageAttachments is the single pre-StartSession validation
// path (design §3.9): (a) raw structure → invalid_params; (b) support matrix
// keyed on the EFFECTIVE kind (never the raw kind) → unsupported_attachment.
// Runs before any session side effect (admission, switchDir, StartSession,
// markRunning, split) so a rejected message leaves no trace.
func validateSendMessageAttachments(agent core.Agent, inputs []AttachmentInput) *WireError {
	if err := validateAttachmentStructure(inputs); err != nil {
		return &WireError{Code: "invalid_params", Message: err.Error()}
	}
	if len(inputs) == 0 {
		return nil
	}
	supported := attachmentSupport(agent)
	for i, a := range inputs {
		kind := classifyAttachment(a)
		if !supported[kind] {
			return &WireError{
				Code: "unsupported_attachment",
				Message: fmt.Sprintf("attachments[%d]: %s attachments are not supported by this backend (effective kind %q)",
					i, kind, classifyAttachment(a)),
			}
		}
	}
	return nil
}

// splitAttachments 把 wire 附件解码成 agent 驱动需要的 image/file 切片。
// 分类与 pre-check 共用 classifyAttachment（同一判断，不复制第二套）。
// 空/invalid/decoded-empty base64 返回 validation error——不再 continue 静默
// 丢弃；调用方（send 路径）将其映射为 invalid_params。正常情况下
// pre-StartSession 校验已先行拦截，这里的 error 是防御性兜底。
func splitAttachments(inputs []AttachmentInput) (images []core.ImageAttachment, files []core.FileAttachment, err error) {
	for i, a := range inputs {
		if a.Base64 == "" {
			return nil, nil, fmt.Errorf("attachments[%d]: base64 payload is required", i)
		}
		data, decErr := base64.StdEncoding.DecodeString(a.Base64)
		if decErr != nil || len(data) == 0 {
			return nil, nil, fmt.Errorf("attachments[%d]: invalid or empty base64 payload", i)
		}
		if classifyAttachment(a) == "image" {
			images = append(images, core.ImageAttachment{
				MimeType: a.Mime,
				Data:     data,
				FileName: a.Filename,
			})
		} else {
			files = append(files, core.FileAttachment{
				MimeType: a.Mime,
				Data:     data,
				FileName: a.Filename,
			})
		}
	}
	return images, files, nil
}
