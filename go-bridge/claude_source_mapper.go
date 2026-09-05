package gobridge

import (
	"encoding/json"
	"fmt"
	"strings"
)

func buildClaudeSourceRecordBatch(
	state ClaudeSourceState,
	record claudeRelayScannedRecord,
	backendID, sessionID, bridgeEpoch string,
	correlation claudeSourceCorrelation,
	fileOrderTurnID string,
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
	events := claudeEntryToProjectionEvents(entry, &currentTurnID, nil)
	claudeTagSourceBlockOrdinals(entry, events)
	partID := turnID
	if entry.Type == "user" {
		partID = strings.TrimSpace(entry.UUID)
	}
	return ClaudeSourceRecordBatch{
		BackendID: backendID, SessionID: sessionID, BridgeEpoch: bridgeEpoch,
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
			case "text":
				meaningful = strings.TrimSpace(block.Text) != ""
			case "thinking":
				meaningful = strings.TrimSpace(block.Thinking) != ""
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
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" && eventIndex < len(events) {
				value := ordinal
				events[eventIndex].SourceBlockOrdinal = &value
				break
			}
		}
	}
}
