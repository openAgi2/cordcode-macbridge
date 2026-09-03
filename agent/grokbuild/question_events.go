package grokbuild

// Follower question events, dual-track shared emission.
//
// The bridge treats wire question_asked/question_resolved as derived legacy
// frames (go-bridge projection_delivery.go: they never enter the projection
// Kernel and are withheld from session_sync_v2 connections). v2 clients only
// see questions through the canonical structured-input face:
// user_input_requested/user_input_resolved projected as user_input parts, and
// resolve_user_input for the reply. dsh-web established that pattern
// (approvals.go); grokbuild mirrors it on both rails.

import (
	"github.com/openAgi2/cordcode-macbridge/core"
)

// askUserQuestionToolName is the tool name in grok's chat_history.jsonl
// tool_calls (the ACP ext_method face is "x.ai/ask_user_question").
const askUserQuestionToolName = "ask_user_question"

// emitQuestionAsked surfaces one pending question on both faces:
//   - canonical EventUserInputRequested (v2 clients, projected user_input part);
//   - legacy EventQuestionAsked (v1 clients).
//
// multiSelect is honored on the canonical face (answerMode multiple); the
// legacy wire has no multiSelect field so v1 keeps single-select.
func emitQuestionAsked(emit func(core.Event), sessionID, questionID, questionText string, opts []core.QuestionOption, multiSelect bool) {
	if emit == nil {
		return
	}
	uiOpts := make([]core.UserInputOption, 0, len(opts))
	for _, o := range opts {
		uiOpts = append(uiOpts, core.UserInputOption{ID: o.ID, Label: o.Label, Description: o.Description})
	}
	mode := core.UserInputAnswerModeSingle
	if multiSelect {
		mode = core.UserInputAnswerModeMultiple
	}
	emit(core.Event{
		Type:      core.EventUserInputRequested,
		SessionID: sessionID,
		ItemID:    questionID,
		UserInput: &core.UserInputInteraction{
			InteractionID: questionID,
			Status:        core.UserInputStatusPending,
			Questions: []core.UserInputQuestion{{
				ID:     questionID,
				Prompt: questionText,
				AnswerMode: mode,
				Options:  uiOpts,
				// grok's TUI modal always carries the freeform "type your answer
				// here" row for model-issued ask_user_question (pager
				// acp_handler/interactions.rs opens the view WITHOUT with_no_freeform
				// — that gate exists only for the SuperGrok upsell modal). A typed
				// answer rides the wire as label "Other" + annotations notes
				// (types.rs AskUserQuestionExtResponse::Accepted), which the answer
				// path mirrors.
				AllowsCustomAnswer: true,
				Required:           true,
			}},
			CanRespond: true,
			CanReject:  true,
		},
	})
	emit(core.Event{
		Type:         core.EventQuestionAsked,
		SessionID:    sessionID,
		QuestionID:   questionID,
		QuestionText: questionText,
		QuestionOpts: opts,
	})
}

// emitQuestionResolved closes one question on both faces. outcome is the wire
// outcome ("accepted"/"cancelled"/"resolved"); source ∈ ios|mac|other_client|backend.
func emitQuestionResolved(emit func(core.Event), sessionID, questionID, outcome, source string) {
	if emit == nil {
		return
	}
	status := core.UserInputStatusAnswered
	switch outcome {
	case "cancelled", "rejected":
		status = core.UserInputStatusRejected
	}
	emit(core.Event{
		Type:      core.EventUserInputResolved,
		SessionID: sessionID,
		ItemID:    questionID,
		UserInput: &core.UserInputInteraction{
			InteractionID:    questionID,
			Status:           status,
			ResolutionSource: source,
		},
	})
	emit(core.Event{
		Type:       core.EventQuestionResolved,
		SessionID:  sessionID,
		QuestionID: questionID,
		Content:    outcome,
	})
}
