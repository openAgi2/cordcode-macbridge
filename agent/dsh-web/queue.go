package dshweb

// Official pending-queue management (session/updateQueue — Typert remote on
// the session controller; S3 management scope, OD-2b=B). Wire shape pinned by
// A3b live evidence (scripts/dshweb-phase0/alpha1-updatequeue-wire.json):
// args {request:{sessionId, itemId, action:{kind, content?}}} → {accepted:true};
// errors verbatim — session/steer-unavailable ("current turn no longer
// accepts steering", commands.ts: target!=='next-turn' || status!=='running')
// and session/queue-item-not-found ("queued item is no longer pending").
// itemId is the official UserMessage.id — the same identity the S3
// placeholder rows are keyed by (A3a id-continuity evidence), so iOS targets
// the row it renders.

import (
	"context"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const sessionUpdateQueueMethod = "session/updateQueue"

// queueActionWire mirrors the official QueueAction union: {kind:'edit',
// content:[text blocks]} | {kind:'remove'} | {kind:'steer'}.
type queueActionWire struct {
	Kind    string              `json:"kind"`
	Content []promptContentPart `json:"content,omitempty"`
}

type updateQueueArgs struct {
	Request struct {
		SessionID string          `json:"sessionId"`
		ItemID    string          `json:"itemId"`
		Action    queueActionWire `json:"action"`
	} `json:"request"`
}

var _ core.SessionQueueManager = (*Agent)(nil)

// UpdateSessionQueue runs one official queue mutation host-side. action ∈
// edit|remove|steer (edit requires non-empty text content — the official
// gateway rejects whitespace-only edits with gateway/bad-request). Errors
// pass through verbatim (坑 7); the seat's own splice journal events carry
// the projection update.
func (a *Agent) UpdateSessionQueue(ctx context.Context, sessionID, itemID, action, content string) error {
	if sessionID == "" {
		return fmt.Errorf("dsh-web: update session queue: empty session id")
	}
	if itemID == "" {
		return fmt.Errorf("dsh-web: update session queue: empty item id")
	}
	var act queueActionWire
	switch action {
	case core.SessionQueueActionEdit:
		if strings.TrimSpace(content) == "" {
			return fmt.Errorf("dsh-web: update session queue: edit requires non-empty text content")
		}
		act = queueActionWire{Kind: action, Content: []promptContentPart{{Type: "text", Text: content}}}
	case core.SessionQueueActionRemove, core.SessionQueueActionSteer:
		act = queueActionWire{Kind: action}
	default:
		return fmt.Errorf("dsh-web: update session queue: unknown action %q", action)
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	var args updateQueueArgs
	args.Request.SessionID = sessionID
	args.Request.ItemID = itemID
	args.Request.Action = act
	return client.Call(ctx, sessionUpdateQueueMethod, args, nil)
}
