package dshweb

// SessionEvent data payload types — COPIED from agent/dsh (design §4.1/M3:
// "复用 codec = 复制映射表进新包，不 import"; the mux session/event frame's
// event field is the same strict-envelope + wide-data shape as the disk log,
// so the data payload types are identical). Source: agent/dsh/{events.go,
// store.go} at dsh/driver round12.

import (
	"encoding/json"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// dshSource is the shared source discriminant (user/plugin/model/tool kinds).
// Form/Summary/SenderSessionID are the subagent-settled settle-notice fields
// (official continuation.ts SubagentSettledMessageSource); absent on every
// other kind — additive, zero risk to existing decodes.
type dshSource struct {
	Kind            string `json:"kind"`
	Plugin          string `json:"plugin,omitempty"`
	CallID          string `json:"callId,omitempty"`
	Form            string `json:"form,omitempty"`
	Summary         string `json:"summary,omitempty"`
	SenderSessionID string `json:"senderSessionId,omitempty"`
}

// dshModelSource extends the source discriminant with the model attribution
// assistant messages carry.
type dshModelSource struct {
	dshSource
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// dshContentBlock is one message content block, mirroring the official
// ContentBlockMap envelope (llm/src/types.ts: text/reasoning/image/file/
// tool-call/tool-addition/tool-removal). The nested-content "tool-result"
// block tag is RETIRED upstream (agent-team/projection.ts:43); tool results
// carry message-level toolCallId/isError with top-level text blocks.
type dshContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// Attachment is the durable reference carried by journal image/file
	// blocks after admission (attachment/index.ts:114-129 admitPromptContent;
	// ImageAttachmentRef / FileAttachmentRef, attachment/src/types.ts).
	Attachment *dshAttachmentRef `json:"attachment,omitempty"`
}

// dshAttachmentRef mirrors the official durable attachment references:
// image blocks carry {attachmentId, mediaType, bytes, width, height, name?}
// (ImageAttachmentRef); file blocks carry {attachmentId, name, bytes}
// (FileAttachmentRef, sha256 content-addressed).
type dshAttachmentRef struct {
	AttachmentID string `json:"attachmentId"`
	MediaType    string `json:"mediaType,omitempty"`
	Name         string `json:"name,omitempty"`
	Bytes        int64  `json:"bytes"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
}

// eventAttachments maps journal image/file blocks onto the wire descriptors
// (dsh-web S4). Text/reasoning/tool blocks contribute nothing.
func eventAttachments(blocks []dshContentBlock) []core.EventAttachment {
	var out []core.EventAttachment
	for _, blk := range blocks {
		ref := blk.Attachment
		if ref == nil || ref.AttachmentID == "" {
			continue
		}
		switch blk.Type {
		case "image":
			out = append(out, core.EventAttachment{
				Kind:         "image",
				AttachmentID: ref.AttachmentID,
				MediaType:    ref.MediaType,
				Name:         ref.Name,
				Bytes:        ref.Bytes,
				Width:        ref.Width,
				Height:       ref.Height,
			})
		case "file":
			out = append(out, core.EventAttachment{
				Kind:         "file",
				AttachmentID: ref.AttachmentID,
				Name:         ref.Name,
				Bytes:        ref.Bytes,
			})
		}
	}
	return out
}

// dshUserMessageData is user/message's data payload.
type dshUserMessageData struct {
	Content []dshContentBlock `json:"content"`
	Source  *dshSource        `json:"source,omitempty"`
	Role    string            `json:"role,omitempty"`
	ID      string            `json:"id,omitempty"`
}

// dshUsage is the token usage snapshot carried by assistant/chunk(usage) and
// assistant/message. inputTokens does NOT include cache hits.
type dshUsage struct {
	InputTokens     int `json:"inputTokens"`
	OutputTokens    int `json:"outputTokens"`
	CacheReadTokens int `json:"cacheReadTokens"`
	ReasoningTokens int `json:"reasoningTokens"`
}

// dshTitleData is session/title's data payload.
type dshTitleData struct {
	Title  string     `json:"title"`
	Source *dshSource `json:"source,omitempty"`
}
