package gobridge

// projection_performance_metrics_test.go — PERF-S0A（iOS 仓
// docs/2026-08-23-message-web-gpuix-borrowing-realistic-assessment.md §13）：
//   1. projection 指标日志 content-free：即使 data/patch 携带真实消息文本，输出也只有
//      计数/字节/revision/前缀，不含内容；
//   2. 关联字段齐备（metricsSchema/backendID/sessionPrefix/recoveryID/sinceRev/headRev/
//      baseRev/syncRev），可与 iOS/Web 层指标按同一 schema 关联。
//
// 只读生产函数，不修改任何 production 行为。

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestProjectionMetricsAreContentFreeAndCorrelatable(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)

	// 唯一标记内容：若任何指标行泄漏正文，会在输出中被捕获。
	const marker = "SENSITIVE_MARKER_content_must_not_leak_0123456789"
	projection := SessionProjection{
		SessionID: "session-metrics-probe-1234",
		SyncRev:   12,
		Turns: []TurnProjection{{
			TurnID: "turn-metrics-1",
			Status: "completed",
			User: &MessageProjection{
				ID:   "msg-user-metrics-1",
				Role: "user",
				Parts: []ProjectionPart{{
					Type:         "text",
					Text:         marker,
					Presentation: "final",
				}},
			},
			Assistant: &MessageProjection{
				ID:   "msg-asst-metrics-1",
				Role: "assistant",
				Parts: []ProjectionPart{{
					Type:       "tool",
					ItemID:     "call-metrics-1",
					ToolName:   "Bash",
					ToolResult: marker,
					ToolStatus: "completed",
				}},
			},
		}},
	}

	// data 里也带标记内容：responseBytes 只应出现尺寸，不出现内容本身。
	data := map[string]interface{}{
		"projection": projection,
		"note":       marker,
	}
	msg := WireMessage{Type: "request", RequestID: "req-perf-1", BackendID: "codex-web"}
	logProjectionResponseMetrics(
		msg, "session-metrics-probe-1234", 0, 12,
		"get_session_projection", data, "recovery-metrics-1", time.Now(), &projection,
	)

	patch := ProjectionPatch{
		BaseRev: 11,
		SyncRev: 12,
		UpsertTurns: []TurnProjection{{
			TurnID: "turn-metrics-1",
			Status: "completed",
			Assistant: &MessageProjection{
				ID:   "msg-asst-metrics-1",
				Role: "assistant",
				Parts: []ProjectionPart{{
					Type: "text",
					Text: marker,
				}},
			},
		}},
	}
	logProjectionPatchMetrics("opencode-web", "session-metrics-probe-1234", "recovery-metrics-1", patch)

	out := logs.String()
	if !strings.Contains(out, "projection performance metrics") || !strings.Contains(out, "projection patch metrics") {
		t.Fatalf("expected both metric log lines; got:\n%s", out)
	}
	if strings.Contains(out, marker) {
		t.Fatalf("metrics log leaked message content:\n%s", out)
	}
	// 完整 sessionID 不得出现（只有 8 字符前缀参与日志）。
	if strings.Contains(out, "session-metrics-probe-1234") {
		t.Fatalf("metrics log leaked full session id:\n%s", out)
	}

	for _, key := range []string{
		"metricsSchema=1",
		"backendID=codex-web",
		"backendID=opencode-web",
		"sessionPrefix=session-",
		"recoveryID=recovery-metrics-1",
		"sinceRev=0",
		"headRev=12",
		"baseRev=11",
		"syncRev=12",
		"responseBytes=",
		"encodedBytes=",
		"turnCount=1",
		"upsertTurnCount=1",
	} {
		if !strings.Contains(out, key) {
			t.Errorf("missing correlation/counter key %q in metrics output:\n%s", key, out)
		}
	}
}

// schema 版本与 iOS（MessageWebPerformanceSampling.metricsSchemaVersion=1）、
// Web（MESSAGE_WEB_PERF_SCHEMA_VERSION=1）三方一致；漂移即测试失败。
func TestProjectionPerformanceMetricsSchemaMatchesCrossLayerVersion(t *testing.T) {
	if projectionPerformanceMetricsSchema != 1 {
		t.Fatalf("projectionPerformanceMetricsSchema drifted: %d (expected 1, matching iOS/Web schema v1)", projectionPerformanceMetricsSchema)
	}
}
