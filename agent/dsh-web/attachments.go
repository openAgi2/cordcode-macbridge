package dshweb

// Durable image read (official session/attachment — S4 receive side, OD-3).
// Wire shape pinned by A4a live evidence
// (scripts/dshweb-phase0/alpha1-attachment-wire.json):
//
//	args {request:{sessionId, attachmentId}}
//	→ value {attachment: ImageAttachmentRef verbatim, data: base64}
//
// Errors pass through verbatim (坑 7) — session/attachment-invalid
// ATTACHMENT_NOT_REFERENCED for ids the session journal does not reference
// (commands.ts:406-411 referencedImage proof), session/not-found for
// disposed sessions. The read is a stateless seat RPC keyed by sessionId —
// no live session binding required, so it lives on the Agent like
// UpdateSessionQueue (queue.go).

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.AttachmentReader = (*Agent)(nil)

// ReadAttachment reads one durable image the session's journal references.
func (a *Agent) ReadAttachment(ctx context.Context, sessionID, attachmentID string) (*core.AttachmentData, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("dsh-web: read attachment: empty session id")
	}
	if attachmentID == "" {
		return nil, fmt.Errorf("dsh-web: read attachment: empty attachment id")
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var val struct {
		Attachment dshAttachmentRef `json:"attachment"`
		Data       string           `json:"data"`
	}
	if err := client.Call(ctx, "session/attachment",
		map[string]any{"request": map[string]any{"sessionId": sessionID, "attachmentId": attachmentID}},
		&val); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(val.Data)
	if err != nil {
		return nil, fmt.Errorf("dsh-web: session/attachment data is not base64: %w", err)
	}
	return &core.AttachmentData{
		Ref: core.EventAttachment{
			Kind:         "image",
			AttachmentID: val.Attachment.AttachmentID,
			MediaType:    val.Attachment.MediaType,
			Name:         val.Attachment.Name,
			Bytes:        val.Attachment.Bytes,
			Width:        val.Attachment.Width,
			Height:       val.Attachment.Height,
		},
		Data: data,
	}, nil
}
