package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// questions.go implements the C6 §6.8 structured-question surface from the
// A7 evidence (1.18.18):
//
//	GET  /question                         → pending requests (same shape as asked)
//	SSE  question.asked                    → {id, sessionID, questions[{question, header, options[{label, description}], multiple}], tool}
//	POST /question/{requestID}/reply       → body {"answers": string[][]} (labels per question)
//	POST /question/{requestID}/reject      → body {}
//	SSE  question.replied / question.rejected → terminal (no question_resolved exists)
//
// Bridge mapping (canonical): asked translates ONCE into the canonical
// user_input_requested payload (Event.UserInput) through the normal live
// route; replied/rejected translate into user_input_resolved with
// resolutionSource=other_client when the SERVER says another client answered.
// The legacy single-question presentation (RespondQuestion) stays
// not_supported — it is not the reply route.

// ocwQuestionOption is one official option row (labels only — the official
// protocol has no option ids).
type ocwQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ocwQuestion is one official question row.
type ocwQuestion struct {
	Question string               `json:"question"`
	Header   string               `json:"header"`
	Options  []ocwQuestionOption `json:"options"`
	Multiple bool                 `json:"multiple"`
}

// ocwQuestionTool is the official tool correlation block (A7): messageID =
// the assistant message of the running turn, callID = the tool item.
type ocwQuestionTool struct {
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// ocwQuestionRequest is the official pending/asked request shape.
type ocwQuestionRequest struct {
	ID        string        `json:"id"`
	SessionID string        `json:"sessionID"`
	Questions []ocwQuestion `json:"questions"`
	Tool      ocwQuestionTool `json:"tool"`
}

func knownToolCallID(req *ocwQuestionRequest) string {
	if req == nil {
		return ""
	}
	return req.Tool.CallID
}

// pendingQuestions tracks live asked requests so ResolveUserInput can map the
// canonical option ids back to official labels. Entries are cleared on
// replied/rejected; a process restart recovers them via GET /question.
func (a *Agent) noteQuestionAsked(req ocwQuestionRequest) {
	a.questionMu.Lock()
	defer a.questionMu.Unlock()
	if a.pendingQuestions == nil {
		a.pendingQuestions = make(map[string]ocwQuestionRequest)
	}
	a.pendingQuestions[req.ID] = req
}

func (a *Agent) questionResolved(id string) (ocwQuestionRequest, bool) {
	a.questionMu.Lock()
	defer a.questionMu.Unlock()
	req, ok := a.pendingQuestions[id]
	if ok {
		delete(a.pendingQuestions, id)
	}
	return req, ok
}

// questionInteraction maps the official asked request to the canonical
// UserInputInteraction payload. Option ids are derived deterministically
// (interactionID/qN/oN — §6.1) so iOS re-renders identically across reloads.
func questionInteraction(req ocwQuestionRequest) *core.UserInputInteraction {
	questions := make([]core.UserInputQuestion, 0, len(req.Questions))
	for qi, q := range req.Questions {
		mode := core.UserInputAnswerModeSingle
		if q.Multiple {
			mode = core.UserInputAnswerModeMultiple
		}
		options := make([]core.UserInputOption, 0, len(q.Options))
		for oi, opt := range q.Options {
			options = append(options, core.UserInputOption{
				ID:          fmt.Sprintf("%s/q%d/o%d", req.ID, qi, oi),
				Label:       opt.Label,
				Description: opt.Description,
			})
		}
		questions = append(questions, core.UserInputQuestion{
			ID:         fmt.Sprintf("%s/q%d", req.ID, qi),
			Header:     q.Header,
			Prompt:     q.Question,
			AnswerMode: mode,
			Options:    options,
			Required:   true,
		})
	}
	return &core.UserInputInteraction{
		InteractionID: req.ID,
		Status:        core.UserInputStatusPending,
		Questions:     questions,
		CanRespond:    true,
		CanReject:     true,
	}
}

// fetchPendingQuestions reads GET /question (A7-proven bare array).
func (a *Agent) fetchPendingQuestions(ctx context.Context, c *Client) ([]ocwQuestionRequest, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/question"), a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("opencode-web: pending questions must be a bare array (generation-118 verified shape), got: %s", truncateForError(string(raw)))
	}
	var rows []ocwQuestionRequest
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("opencode-web: pending questions malformed: %w", err)
	}
	out := make([]ocwQuestionRequest, 0, len(rows))
	for i, req := range rows {
		if req.ID == "" {
			return nil, fmt.Errorf("opencode-web: pending question row %d missing required id", i)
		}
		out = append(out, req)
	}
	return out, nil
}

// lookupQuestion returns the live request shape for an interaction id,
// recovering from GET /question when the in-memory pending map missed
// (restart, or an asked frame this process never saw).
func (a *Agent) lookupQuestion(ctx context.Context, c *Client, interactionID string) (*ocwQuestionRequest, error) {
	a.questionMu.Lock()
	req, ok := a.pendingQuestions[interactionID]
	a.questionMu.Unlock()
	if ok {
		return &req, nil
	}
	pending, err := a.fetchPendingQuestions(ctx, c)
	if err != nil {
		return nil, err
	}
	for _, p := range pending {
		a.noteQuestionAsked(p)
		if p.ID == interactionID {
			return &p, nil
		}
	}
	return nil, &core.UserInputError{
		Code:    "interaction_not_found",
		Message: "the server has no pending question with that id (it may already be answered by another client)",
	}
}

// ResolveUserInput implements core.UserInputResponder: answer/reject through
// the official routes. The serve holds the single answer lock — a reply that
// loses the race surfaces the serve's error honestly.
func (a *Agent) ResolveUserInput(ctx context.Context, interactionID string, clientActionID string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return core.UserInputResolution{}, err
	}
	req, err := a.lookupQuestion(ctx, c, interactionID)
	if err != nil {
		return core.UserInputResolution{}, err
	}

	if action == core.UserInputActionReject {
		path := c.apiPath("/question/") + interactionID + "/reject"
		code, raw, err := c.doRequest(ctx, http.MethodPost, c.endpoint(path), map[string]any{}, a.GetWorkDir(), true)
		if err != nil {
			return core.UserInputResolution{}, fmt.Errorf("opencode-web question reject: %w", err)
		}
		if code == 404 || code == 409 {
			a.questionResolved(interactionID)
			return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusRejected}, nil
		}
		if code >= 400 {
			return core.UserInputResolution{}, fmt.Errorf("opencode-web question reject HTTP %d: %s", code, truncateForError(string(raw)))
		}
		a.questionResolved(interactionID)
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusRejected}, nil
	}

	// Answer: map canonical answers onto the official string[][] (labels per
	// question, question order preserved; unanswered questions submit []).
	body := make([][]string, len(req.Questions))
	for i := range body {
		body[i] = []string{}
	}
	for _, ans := range answers {
		qIdx := -1
		for i := range req.Questions {
			if fmt.Sprintf("%s/q%d", interactionID, i) == ans.QuestionID {
				qIdx = i
				break
			}
		}
		if qIdx == -1 {
			return core.UserInputResolution{}, &core.UserInputError{
				Code:    "invalid_answer",
				Message: "answer references an unknown question of this interaction",
			}
		}
		values := make([]string, 0, len(ans.Values))
		for _, v := range ans.Values {
			switch v.Kind {
			case core.UserInputValueOption:
				label, ok := optionLabelFor(req.Questions[qIdx], v.OptionID)
				if !ok {
					return core.UserInputResolution{}, &core.UserInputError{
						Code:    "invalid_answer",
						Message: "answer references an unknown option of this question",
					}
				}
				values = append(values, label)
			case core.UserInputValueText:
				if strings.TrimSpace(v.Text) == "" {
					return core.UserInputResolution{}, &core.UserInputError{
						Code:    "invalid_answer",
						Message: "custom answer value is empty",
					}
				}
				values = append(values, v.Text)
			}
		}
		body[qIdx] = values
	}
	for i, slot := range body {
		if len(slot) == 0 {
			return core.UserInputResolution{}, &core.UserInputError{
				Code:    "invalid_answer",
				Message: fmt.Sprintf("question %d has no answer (the official reply requires a value per question)", i),
			}
		}
	}

	path := c.apiPath("/question/") + interactionID + "/reply"
	code, raw, err := c.doRequest(ctx, http.MethodPost, c.endpoint(path), map[string]any{"answers": body}, a.GetWorkDir(), true)
	if err != nil {
		return core.UserInputResolution{}, fmt.Errorf("opencode-web question reply: %w", err)
	}
	if code == 404 || code == 409 {
		a.questionResolved(interactionID)
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
	}
	if code >= 400 {
		return core.UserInputResolution{}, fmt.Errorf("opencode-web question reply HTTP %d: %s", code, truncateForError(string(raw)))
	}
	a.questionResolved(interactionID)
	return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
}

func optionLabelFor(q ocwQuestion, optionID string) (string, bool) {
	// Option ids are "<interactionID>/qN/oM" (questionInteraction); only the
	// trailing oM indexes this question's options.
	parts := strings.Split(optionID, "/")
	if len(parts) == 0 {
		return "", false
	}
	tail := parts[len(parts)-1]
	for oi, opt := range q.Options {
		if fmt.Sprintf("o%d", oi) == tail {
			return opt.Label, true
		}
	}
	return "", false
}

// StructuredUserInputReady implements core.StructuredUserInputProvider.
func (a *Agent) StructuredUserInputReady() bool { return true }

var _ core.UserInputResponder = (*Agent)(nil)
var _ core.StructuredUserInputProvider = (*Agent)(nil)

