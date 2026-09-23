package core

import "context"

// SessionQueueManager is an optional agent interface for backends that expose
// the official pending-queue management surface (dsh-web session/updateQueue,
// OD-2b=B). Actions mirror the official QueueAction union: edit (text content
// only), remove, steer — each targets ONE pending item by its official
// UserMessage.id (the same id the S3 placeholder rows are keyed by). The
// seat's own errors pass through verbatim (session/steer-unavailable,
// session/queue-item-not-found — A3b live evidence).
type SessionQueueManager interface {
	UpdateSessionQueue(ctx context.Context, sessionID, itemID, action, content string) error
}

// Session queue management actions (official QueueAction kinds).
const (
	SessionQueueActionEdit   = "edit"
	SessionQueueActionRemove = "remove"
	SessionQueueActionSteer  = "steer"
)

// AttachmentReader is an optional agent interface for backends that can read
// one durable image attachment by its official id (dsh-web S4: session/
// attachment — referencedImage journal-proof + readImage; the id comes from
// the journal image block carried on the user message). Absence of the
// interface means the backend has no attachment read path and the bridge
// must not advertise the get_attachment RPC for it.
type AttachmentReader interface {
	// ReadAttachment returns the official ImageAttachmentRef verbatim plus
	// the decoded image bytes for one session-referenced attachment.
	ReadAttachment(ctx context.Context, sessionID, attachmentID string) (*AttachmentData, error)
}

// SessionUnarchiver is an optional agent interface for backends that can
// restore an archived session (dsh-web S5, OD-1=A: official
// workspace/unarchiveSession — idempotent, unknown/not-archived ids succeed
// as no-ops). Absence means the backend has no unarchive path and the bridge
// must not advertise the unarchive_session RPC for it.
type SessionUnarchiver interface {
	// UnarchiveSession removes one session from the backend's archive set.
	// The returned AgentSessionInfo is identity + summary (ArchivedAt zero).
	UnarchiveSession(ctx context.Context, sessionID string) (*AgentSessionInfo, error)
}
