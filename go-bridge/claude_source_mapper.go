package gobridge

import (
	"encoding/json"
	"fmt"
	"strings"
)

// claudeSourceRowContentClass 是内容行的三分类（修复方案 v2.1 §5.1）：
// 可投影 / 已证明无展示（白名单，唯一可走 source-only）/ 不支持。
type claudeSourceRowContentClass int

const (
	claudeRowContentProjectable claudeSourceRowContentClass = iota
	claudeRowContentProvenNoDisplay
	claudeRowContentUnsupported
)

func (c claudeSourceRowContentClass) String() string {
	switch c {
	case claudeRowContentProjectable:
		return "projectable"
	case claudeRowContentProvenNoDisplay:
		return "proven_no_display"
	default:
		return "unsupported"
	}
}

// claudeBlockIsBlankDisplay 是 text/thinking 块的「无展示」判断——mapper 发事件
// 条件与分类器白名单共用这同一函数（v2.1 同源约束，防两者漂移）。非 text/thinking
// 块恒非空白。
func claudeBlockIsBlankDisplay(block claudeRelayContentBlock) bool {
	switch block.Type {
	case "text":
		return strings.TrimSpace(block.Text) == ""
	case "thinking":
		return strings.TrimSpace(block.Thinking) == ""
	default:
		return false
	}
}

// classifyClaudeSourceRowContent 按块三分类内容行（v2.1 §5.1，样本依据 §3.2：
// K1/K2 四形态 3,515 行 0 违例）。终态判定复用生产 isFinalClaudeStopReason，
// 不另立集合（复审 R2 实施约束）。
func classifyClaudeSourceRowContent(entry claudeTranscriptRelayEntry) claudeSourceRowContentClass {
	if entry.Message == nil {
		// 无 message 的 admitted 行（与 buildClaudeSourceRecordBatch 的结构性
		// 检查同界）按零事件处理 → 不支持。
		return claudeRowContentUnsupported
	}
	if entry.Type != "assistant" {
		// user 行：含 tool_result 或归一化文本非空 → 可投影；零事件 user 行
		//（未观测）→ 不支持。
		blocks := claudeRelayContentBlocks(entry.Message.Content)
		for _, b := range blocks {
			if b.Type == "tool_result" {
				return claudeRowContentProjectable
			}
		}
		if strings.TrimSpace(claudeNormalizedUserText(blocks)) != "" {
			return claudeRowContentProjectable
		}
		return claudeRowContentUnsupported
	}
	// assistant 行：API error 与终态（含空白终态行——T3 守护：仍发 turn_completed）
	// 恒可投影。
	if entry.IsAPIErrorMessage || isFinalClaudeStopReason(entry.Message.StopReason) {
		return claudeRowContentProjectable
	}
	blocks := claudeRelayContentBlocks(entry.Message.Content)
	if len(blocks) == 0 {
		return claudeRowContentUnsupported
	}
	hasProjectable := false
	for _, b := range blocks {
		switch b.Type {
		case "text", "thinking":
			if !claudeBlockIsBlankDisplay(b) {
				hasProjectable = true
			}
		case "tool_use", "server_tool_use":
			hasProjectable = true
		default:
			// 未知块类型（redacted_thinking / image / 未来新块，未观测）——
			// 失败关闭：不支持（v2.1 F-01）。
			return claudeRowContentUnsupported
		}
	}
	if hasProjectable {
		return claudeRowContentProjectable
	}
	// 白名单：每个块都是空白 thinking（signature 有无均可）或空白 text，
	// stop 非终态，非 API error 行。
	return claudeRowContentProvenNoDisplay
}

// claudeSourceRowShapeSummary 生成拒收诊断用的行形态（v2.1 §5.2）：type、各块
// 类型及是否空白、stop、rowClass。不包含正文、signature、工具输入。
func claudeSourceRowShapeSummary(entry claudeTranscriptRelayEntry, rowClass claudeSourceRowContentClass) string {
	blockShapes := make([]string, 0, 4)
	for _, b := range claudeRelayContentBlocks(entry.Message.Content) {
		shape := b.Type
		if b.Type == "text" || b.Type == "thinking" {
			if claudeBlockIsBlankDisplay(b) {
				shape += ":blank"
			} else {
				shape += ":nonblank"
			}
		}
		blockShapes = append(blockShapes, shape)
	}
	stop := "null"
	if entry.Message != nil && entry.Message.StopReason != "" {
		stop = entry.Message.StopReason
	}
	return fmt.Sprintf("rowClass=%s type=%s blocks=[%s] stop=%s",
		rowClass, entry.Type, strings.Join(blockShapes, ","), stop)
}

func buildClaudeSourceRecordBatch(
	state ClaudeSourceState,
	record claudeRelayScannedRecord,
	backendID, sessionID, bridgeEpoch string,
	correlation claudeSourceCorrelation,
	fileOrderTurnID string,
	oracle claudeAnswerabilityOracle,
) (ClaudeSourceRecordBatch, error) {
	entry := record.Entry
	if entry.UUID == "" || entry.Message == nil ||
		(entry.Type != "user" && entry.Type != "assistant") {
		return ClaudeSourceRecordBatch{}, fmt.Errorf(
			"%w: Claude source mapper unsupported structural row %q",
			ErrProjectionCheckpointInvalid, entry.Type,
		)
	}
	turnID, err := claudeGraphResolvedTurn(state, entry, fileOrderTurnID)
	if err != nil {
		return ClaudeSourceRecordBatch{}, err
	}
	contentBlocks, err := claudeRawContentBlocks(entry.Message.Content)
	if err != nil {
		return ClaudeSourceRecordBatch{}, err
	}
	lifecycle, err := json.Marshal(map[string]interface{}{
		"role":        entry.Message.Role,
		"stop_reason": entry.Message.StopReason,
	})
	if err != nil {
		return ClaudeSourceRecordBatch{}, err
	}
	currentTurnID := turnID
	// live source batch 是 Kernel-entering 调用边（source-batch transaction 是 Claude
	// 内容唯一投影写者）——传真实 oracle（设计 v6 §4.2）。
	events := claudeEntryToProjectionEvents(entry, &currentTurnID, nil, oracle)
	claudeTagSourceBlockOrdinals(entry, events)
	partID := turnID
	if entry.Type == "user" {
		partID = strings.TrimSpace(entry.UUID)
	}
	rowClass := classifyClaudeSourceRowContent(entry)
	return ClaudeSourceRecordBatch{
		BackendID: backendID, SessionID: sessionID, BridgeEpoch: bridgeEpoch,
		// RC-A（v2.1 §5.1）：分类由 mapper 边的纯函数判定；source-only 的
		// 决定仍落在 ApplyClaudeSourceRecordBatch 事务内（唯一 writer）。
		RowClass:     rowClass,
		RowDiagnostic: claudeSourceRowShapeSummary(entry, rowClass),
		Record: ClaudeSourceRecordTransition{
			LogicalRecordUUID: entry.UUID,
			ParentUUID:        entry.ParentUUID,
			StructuralKind:    entry.Type,
			GraphResolvedTurn: turnID,
			SegmentStableKey:  correlation.SegmentStableKey,
			SegmentGeneration: correlation.SegmentGeneration,
			SourceGeneration:  state.SourceGeneration,
			RawByteStart:      record.ByteStart,
			RawByteEnd:        record.ByteEnd,
			ContentBlocks:     contentBlocks,
			SemanticLifecycle: lifecycle,
			Contribution: ClaudeProjectionContribution{
				TurnID: turnID,
				PartID: partID,
			},
		},
		Events: events,
	}, nil
}

func claudeGraphResolvedTurn(
	state ClaudeSourceState,
	entry claudeTranscriptRelayEntry,
	fileOrderTurnID string,
) (string, error) {
	if entry.Type == "user" {
		identity := claudeEntryTurnIdentity(entry)
		if identity == "" {
			return "", fmt.Errorf("%w: Claude user row has no identity", ErrProjectionCheckpointInvalid)
		}
		// Tool-result rows remain owned by their nearest prior user ancestor; ordinary user text
		// establishes a new turn.
		hasUserText := strings.TrimSpace(claudeNormalizedUserText(claudeRelayContentBlocks(entry.Message.Content))) != ""
		if hasUserText {
			return identity, nil
		}
	}
	parent := strings.TrimSpace(entry.ParentUUID)
	visited := make(map[string]struct{})
	for parent != "" {
		if _, duplicate := visited[parent]; duplicate {
			break
		}
		visited[parent] = struct{}{}
		occurrences := state.GraphNodes[parent]
		if len(occurrences) == 0 {
			break
		}
		latest := occurrences[len(occurrences)-1]
		if latest.GraphResolvedTurn != "" {
			return latest.GraphResolvedTurn, nil
		}
		parent = latest.ParentUUID
	}
	// Live-ingest continuity: the source ledger must advance past every physical row (an untracked
	// row would gap the cursor fence, guardrail #6), so a row with no admitted parent-chain user
	// owner falls back to the caller's file-order turn rather than erroring. This is NOT content
	// refereeing (guardrail #4) — it is the pre-existing file-order attribution, used only when graph
	// resolution has no admitted owner (e.g. sidechain/branch rows, pending IR-1b). Graph ownership
	// (H4) still wins whenever a parent-chain owner exists.
	if fileOrderTurnID != "" {
		return fileOrderTurnID, nil
	}
	return "", fmt.Errorf(
		"%w: Claude row %q has no admitted parent-chain user owner",
		ErrProjectionCheckpointInvalid, entry.UUID,
	)
}

func claudeRawContentBlocks(raw json.RawMessage) ([]json.RawMessage, error) {
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		block, marshalErr := json.Marshal(map[string]interface{}{"type": "text", "text": text})
		if marshalErr != nil {
			return nil, marshalErr
		}
		return []json.RawMessage{block}, nil
	}
	return nil, fmt.Errorf("%w: invalid Claude content blocks", ErrProjectionCheckpointInvalid)
}

func claudeTagSourceBlockOrdinals(
	entry claudeTranscriptRelayEntry,
	events []projectionHydrateEvent,
) {
	blocks := claudeRelayContentBlocks(entry.Message.Content)
	eventIndex := 0
	for ordinal, block := range blocks {
		meaningful := false
		if entry.Type == "user" {
			meaningful = block.Type == "tool_result"
		} else {
			switch block.Type {
			case "text", "thinking":
				// v2.1 同源约束：与分类器/mapper 共用同一空白判断。
				meaningful = !claudeBlockIsBlankDisplay(block)
			case "tool_use", "server_tool_use":
				meaningful = true
			}
		}
		if !meaningful || eventIndex >= len(events) {
			continue
		}
		value := ordinal
		events[eventIndex].SourceBlockOrdinal = &value
		eventIndex++
	}
	if entry.Type == "user" {
		// A normal text user row emits one aggregate user_message after any tool-result events.
		for ordinal, block := range blocks {
			if block.Type == "text" && !claudeBlockIsBlankDisplay(block) && eventIndex < len(events) {
				value := ordinal
				events[eventIndex].SourceBlockOrdinal = &value
				break
			}
		}
	}
}
