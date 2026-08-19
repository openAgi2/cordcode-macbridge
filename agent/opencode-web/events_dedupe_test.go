package opencodeweb

import (
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Live topology (2026-08-19 runtime log, ses_fef7): a live session is decoded
// by BOTH a dedicated subscriber (StartSession → relay channel) and the global
// passive one. Every SSE failure frame therefore maps twice, and the trailing
// assistant message.updated(info.error) used to re-arm the per-subscriber
// terminal note AFTER emitResultOnce consumed it — the 套餐 error text flushed
// 3× as text_delta (revs 59/61/63) and rendered 3× on iOS. These tests pin the
// agent-level once-claims across the whole dual-subscriber topology.

func dualFailureFrames() []string {
	return []string{
		sseFrame("message.updated", map[string]any{
			"info": map[string]any{"id": "msg_u1", "role": "user",
				"parts": []any{map[string]any{"type": "text", "text": "讲个天鹅笑话"}}},
			"sessionID": "ses_1",
		}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_1", "status": map[string]any{"type": "busy"}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_1", "status": map[string]any{
			"type": "retry", "attempt": 1, "message": "当前订阅套餐暂未开放GLM-5.2-Highspeed权限",
			"next": 1787109137538}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_1", "status": map[string]any{
			"type": "retry", "attempt": 2, "message": "当前订阅套餐暂未开放GLM-5.2-Highspeed权限",
			"next": 1787109141613}}),
		sseFrame("session.error", map[string]any{"sessionID": "ses_1",
			"error": map[string]any{"name": "APIError", "data": map[string]any{
				"message": "当前订阅套餐暂未开放GLM-5.2-Highspeed权限", "statusCode": 403, "isRetryable": false}}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_1", "status": map[string]any{"type": "idle"}}),
		sseFrame("session.idle", map[string]any{"sessionID": "ses_1"}),
		// Live-pinned trailing frame: the failed assistant message carries the
		// same error under info.error and parts=null (errlab capture sse2.txt).
		sseFrame("message.updated", map[string]any{
			"info": map[string]any{"id": "msg_a1", "role": "assistant",
				"error": map[string]any{"name": "APIError", "data": map[string]any{
					"message": "当前订阅套餐暂未开放GLM-5.2-Highspeed权限", "statusCode": 403}}},
			"sessionID": "ses_1",
		}),
	}
}

// driveInterleaved mirrors the live topology: both subscribers observe each
// SSE frame (dedicated + global connections receive the same stream), not one
// full turn after the other.
func driveInterleaved(sub1, sub2 *sseSubscriber, frames ...string) {
	for _, frame := range frames {
		driveFrames(sub1, frame)
		driveFrames(sub2, frame)
	}
}

func TestTerminalErrorTextEmittedExactlyOnceAcrossDualSubscribers(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub1 := newDrivenSubscriber(t, agent)
	sub2 := newDrivenSubscriber(t, agent)

	driveInterleaved(sub1, sub2, dualFailureFrames()...)

	textCount := 0
	resultErrors := 0
	for _, sub := range []*sseSubscriber{sub1, sub2} {
		for _, ev := range drain(sub) {
			switch ev.Type {
			case core.EventText:
				if strings.Contains(ev.Content, "当前订阅套餐暂未开放GLM-5.2-Highspeed权限") {
					textCount++
				}
			case core.EventResult:
				if ev.Error != nil && strings.Contains(ev.Error.Error(), "当前订阅套餐暂未开放GLM-5.2-Highspeed权限") {
					resultErrors++
				}
			}
		}
	}
	if textCount != 1 {
		t.Fatalf("terminal error text must be emitted exactly once across BOTH subscribers "+
			"(dedicated + passive decode the same frames; was 3× live), got %d", textCount)
	}
	if resultErrors == 0 {
		t.Fatal("at least one subscriber must still surface EventResult with the verbatim error")
	}
}

func TestRetryStatusEmittedOncePerAttemptAcrossDualSubscribers(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub1 := newDrivenSubscriber(t, agent)
	sub2 := newDrivenSubscriber(t, agent)

	driveInterleaved(sub1, sub2, dualFailureFrames()...)

	byAttempt := map[int]int{}
	for _, sub := range []*sseSubscriber{sub1, sub2} {
		for _, ev := range drain(sub) {
			if ev.Type == core.EventRetryStatus {
				byAttempt[ev.RetryAttempt]++
				if ev.Content == "" {
					t.Fatalf("retry status must carry the provider message, got empty (attempt %d)", ev.RetryAttempt)
				}
				if ev.RetryNext == 0 {
					t.Fatalf("retry status must carry the serve next-attempt timestamp, got 0 (attempt %d)", ev.RetryAttempt)
				}
			}
		}
	}
	if byAttempt[1] != 1 || byAttempt[2] != 1 {
		t.Fatalf("each retry attempt must be emitted exactly once across both subscribers, got %v", byAttempt)
	}
}

// A second turn in the same session must re-arm the once-claims: its failure
// text surfaces again (once), not zero (claim stuck) nor thrice.
func TestTerminalClaimRearmsOnNextTurn(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	driveFrames(sub, dualFailureFrames()...)
	drain(sub)

	driveFrames(sub,
		sseFrame("message.updated", map[string]any{
			"info": map[string]any{"id": "msg_u2", "role": "user",
				"parts": []any{map[string]any{"type": "text", "text": "再讲一个"}}},
			"sessionID": "ses_1",
		}),
		sseFrame("session.error", map[string]any{"sessionID": "ses_1",
			"error": map[string]any{"name": "APIError", "data": map[string]any{
				"message": "第二个错误", "statusCode": 429, "isRetryable": false}}}),
		sseFrame("session.idle", map[string]any{"sessionID": "ses_1"}),
	)

	textCount := 0
	for _, ev := range drain(sub) {
		if ev.Type == core.EventText && strings.Contains(ev.Content, "第二个错误") {
			textCount++
		}
	}
	if textCount != 1 {
		t.Fatalf("new turn must re-arm the terminal-text claim exactly once, got %d", textCount)
	}
}
