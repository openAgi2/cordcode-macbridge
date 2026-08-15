package core

import "fmt"

// DeliveryStage classifies the transport fate of one AgentSession.Send call.
// Drivers whose backing transport cannot prove non-delivery (the DSH runtime
// enqueues a prompt server-side BEFORE returning its receipt) return a typed
// *DeliveryError so the bridge can apply the at-most-once delivery matrix by
// errors.As — never by string matching (design docs/2026-08-13-dsh-driver-design.md
// §3.6.3③ Go contract).
type DeliveryStage uint8

const (
	// StagePreWrite: provably undelivered — the session was dead at the
	// pre-send health check, or the write moved zero bytes before failing.
	// The caller may tear the session down, respawn, and send ONCE (a
	// pre-send repair, not a retry).
	StagePreWrite DeliveryStage = iota
	// StagePartialWrite: some bytes were written before the transport failed.
	// The request may have been delivered; the caller must fail visibly and
	// must not replay this prompt.
	StagePartialWrite
	// StageAwaitingResponse: the request was fully written but the response
	// never arrived (timeout / process exit / shutdown). The server may have
	// already enqueued and executed the prompt; replay is forbidden.
	StageAwaitingResponse
	// StageAcceptedUnknown: whether the server accepted the request cannot be
	// determined. Treated exactly like StageAwaitingResponse — fail visibly,
	// no replay.
	StageAcceptedUnknown
)

func (s DeliveryStage) String() string {
	switch s {
	case StagePreWrite:
		return "pre_write"
	case StagePartialWrite:
		return "partial_write"
	case StageAwaitingResponse:
		return "awaiting_response"
	case StageAcceptedUnknown:
		return "accepted_unknown"
	default:
		return fmt.Sprintf("delivery_stage(%d)", uint8(s))
	}
}

// DeliveryError is the typed transport-fate error returned by AgentSession
// implementations that support delivery classification (DSH driver). The
// bridge branches on Stage via errors.As; a plain error means the failure is
// definite (e.g. an explicit server rejection) and carries no replay hazard
// by itself.
type DeliveryError struct {
	Stage DeliveryStage
	Cause error
}

func (e *DeliveryError) Error() string {
	if e == nil {
		return "delivery error"
	}
	if e.Cause == nil {
		return fmt.Sprintf("delivery error at stage %s", e.Stage)
	}
	return fmt.Sprintf("delivery error at stage %s: %v", e.Stage, e.Cause)
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ReplayAllowed reports whether the delivery matrix permits rebuilding the
// session and sending the SAME prompt once more: only a provably undelivered
// pre-write failure does.
func (e *DeliveryError) ReplayAllowed() bool {
	return e != nil && e.Stage == StagePreWrite
}
