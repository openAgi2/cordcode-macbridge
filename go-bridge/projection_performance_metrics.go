package gobridge

import (
	"encoding/json"
	"log/slog"
	"time"
)

// PERF-S0A（iOS 仓 docs/2026-08-23-message-web-gpuix-borrowing-realistic-assessment.md §13）：
// projection 性能指标 schema 版本。字段变化时递增并记录；before/after 对比必须同版本。
const projectionPerformanceMetricsSchema = 1

type projectionPayloadBreakdown struct {
	ProjectionBytes  int
	ExecutionBytes   int
	TurnsBytes       int
	TextBytes        int
	ReasoningBytes   int
	ToolResultBytes  int
	FileChangesBytes int
	TurnCount        int
	PartCount        int
}

func measureProjectionPayload(projection SessionProjection) projectionPayloadBreakdown {
	result := projectionPayloadBreakdown{
		ProjectionBytes: encodedJSONSize(projection),
		ExecutionBytes:  encodedJSONSize(projection.Execution),
		TurnsBytes:      encodedJSONSize(projection.Turns),
		TurnCount:       len(projection.Turns),
	}
	for _, turn := range projection.Turns {
		for _, message := range []*MessageProjection{turn.User, turn.Assistant, turn.System} {
			if message == nil {
				continue
			}
			for _, part := range message.Parts {
				result.PartCount++
				switch part.Type {
				case "text":
					result.TextBytes += len([]byte(part.Text))
				case "reasoning":
					result.ReasoningBytes += len([]byte(part.Text))
				case "tool":
					result.ToolResultBytes += encodedJSONSize(part.ToolResult)
					result.FileChangesBytes += encodedJSONSize(part.FileChanges)
				case "file":
					result.FileChangesBytes += encodedJSONSize(part)
				}
			}
		}
	}
	return result
}

func encodedJSONSize(value interface{}) int {
	if value == nil {
		return 0
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(encoded)
}

func logProjectionResponseMetrics(
	msg WireMessage,
	sessionID string,
	sinceRev, headRev int,
	kind string,
	data interface{},
	recoveryID string,
	startedAt time.Time,
	projection *SessionProjection,
) {
	response := map[string]interface{}{
		"type":      "result",
		"requestId": msg.RequestID,
		"ok":        true,
		"data":      data,
	}
	attrs := []any{
		"metricsSchema", projectionPerformanceMetricsSchema,
		"requestId", msg.RequestID,
		"backendID", msg.BackendID,
		"sessionPrefix", projectionSessionLogPrefix(sessionID),
		"recoveryID", recoveryID,
		"sinceRev", sinceRev,
		"headRev", headRev,
		"responseKind", kind,
		"responseBytes", encodedJSONSize(response),
		"elapsedMs", durationMillis(time.Since(startedAt)),
	}
	if result, ok := data.(map[string]interface{}); ok {
		if resume, ok := result["resume"].(ProjectionResumeDiagnostic); ok {
			attrs = append(attrs, "resumeKind", resume.Kind)
			if resume.Reason != nil {
				attrs = append(attrs, "resumeReason", *resume.Reason)
			}
		}
	}
	if projection != nil {
		breakdown := measureProjectionPayload(*projection)
		attrs = append(attrs,
			"projectionBytes", breakdown.ProjectionBytes,
			"executionBytes", breakdown.ExecutionBytes,
			"turnsBytes", breakdown.TurnsBytes,
			"textBytes", breakdown.TextBytes,
			"reasoningBytes", breakdown.ReasoningBytes,
			"toolResultBytes", breakdown.ToolResultBytes,
			"fileChangesBytes", breakdown.FileChangesBytes,
			"turnCount", breakdown.TurnCount,
			"partCount", breakdown.PartCount,
		)
	}
	slog.Info("go-bridge: projection performance metrics", attrs...)
}

func logProjectionPatchMetrics(backendID, sessionID, recoveryID string, patch ProjectionPatch) {
	upsertBytes := encodedJSONSize(patch.UpsertTurns)
	partOpsBytes := encodedJSONSize(patch.PartOps)
	slog.Info("go-bridge: projection patch metrics",
		"metricsSchema", projectionPerformanceMetricsSchema,
		"backendID", backendID,
		"sessionPrefix", projectionSessionLogPrefix(sessionID),
		"recoveryID", recoveryID,
		"baseRev", patch.BaseRev,
		"syncRev", patch.SyncRev,
		"encodedBytes", encodedJSONSize(patch),
		"executionPresent", patch.Execution != nil,
		"upsertTurnCount", len(patch.UpsertTurns),
		"upsertTurnsBytes", upsertBytes,
		"partOpCount", len(patch.PartOps),
		"partOpsBytes", partOpsBytes,
		"replacesClientIDCount", len(patch.ReplacesClientIDs),
	)
}
