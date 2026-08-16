package dshweb

// Approval/question responders — §8-4 wires the real pending registries and
// the batch-answer assembly (per-question dsh ids keyed by the mux frame's
// rpcId; one /api/respond per completed batch). Until then these return an
// honest error: nothing can have a pending id before the mux stream lands.

import (
	"context"
	"fmt"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// respondApproval maps the bridge permission decision onto the official
// approval response payload (allow→allowed-once / deny→rejected — the
// official outcome set is binary; the wire-level always variants already
// collapse to these two on iOS, design §4.3.4 R3-2).
func (a *Agent) respondApproval(ctx context.Context, sessionID, requestID string, result core.PermissionResult) error {
	return fmt.Errorf("dsh-web: approval responding is not wired yet (design §8-4)")
}

// respondQuestion accumulates one per-question answer of an ask batch
// (R3-1: questionId is dsh's per-question id; answers assemble by id and the
// batch responds once via /api/respond when complete).
func (a *Agent) respondQuestion(ctx context.Context, sessionID, questionID string, optionIDs []string, custom string) error {
	return fmt.Errorf("dsh-web: question responding is not wired yet (design §8-4)")
}

// rejectQuestion cancels the whole batch via the respond error branch
// (ok:false, code "cancelled" — asymmetric with approvals, §4.3.4).
func (a *Agent) rejectQuestion(ctx context.Context, sessionID, questionID string) error {
	return fmt.Errorf("dsh-web: question rejecting is not wired yet (design §8-4)")
}
