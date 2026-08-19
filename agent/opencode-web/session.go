package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// session.go implements the active-turn surface (design §4.3.4): create /
// resume, prompt WITH a catalog model, generation-aware abort, and SSE-bound
// teardown. The server session NEVER kills `opencode serve` — that process is
// global and Swift-managed.
type serverSession struct {
	a      *Agent
	client *Client

	model  atomic.Value // *ocwModelRef — send model resolved at Send time
	chatID atomic.Value // string — serve session id (ses_…)

	sub *sseSubscriber // dedicated, session-filtered

	ctx    context.Context
	cancel context.CancelFunc
	alive  atomic.Bool
}

// StartSession implements core.Agent: resume = bind the known id; new = create
// lazily on first Send (ensureServerSession). Both bind a dedicated filtered
// SSE subscriber so no other session's events leak.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	s := &serverSession{
		a:      a,
		client: c,
		ctx:    sessionCtx,
		cancel: cancel,
	}
	if sessionID != "" && sessionID != core.ContinueSession {
		s.chatID.Store(sessionID)
		// Resume: adopt the serve's own session model as the fallback send
		// model (truth from the server, never a local default).
		if info, err := a.fetchSessionInfo(ctx, c, sessionID); err == nil && info.Model != nil && info.Model.ID != "" {
			s.model.Store(&ocwModelRef{ProviderID: info.Model.ProviderID, ID: info.Model.ID})
		}
	}
	if pending := a.GetModel(); pending != "" {
		providerID, modelID := parseQualifiedModel(pending)
		if modelID != "" {
			s.model.Store(&ocwModelRef{ProviderID: providerID, ID: modelID})
		}
	}
	s.alive.Store(true)

	sub := newSSESubscriber(sessionCtx, a, c)
	sub.sessionFilter.Store(s.CurrentSessionID())
	sub.filterActive.Store(true)
	if err := sub.connect(); err != nil {
		cancel()
		return nil, fmt.Errorf("opencode-web session: SSE connect: %w", err)
	}
	s.sub = sub
	go func() {
		<-sessionCtx.Done()
		_ = sub.Close()
	}()
	// Re-attach replay: iOS 锁屏/后台窗口会错过瞬态 session_retry_status
	//（不做离线持久化，官方 web 同语义）。重附时若快照新鲜（2 分钟内、回合
	// 未收口）则重放一次，回前台/重开会话即见「自动重试中（第 N 次）」。
	if id := s.CurrentSessionID(); id != "" {
		if snap, ok := a.replayableRetrySnapshot(id); ok {
			sub.emit(core.Event{
				Type:         core.EventRetryStatus,
				Content:      snap.Message,
				SessionID:    id,
				RetryAttempt: snap.Attempt,
				RetryNext:    snap.Next,
			})
		}
	}
	return s, nil
}

func (s *serverSession) CurrentSessionID() string {
	if v, ok := s.chatID.Load().(string); ok {
		return v
	}
	return ""
}

func (s *serverSession) Events() <-chan core.Event { return s.sub.events }
func (s *serverSession) Alive() bool               { return s.alive.Load() }

// resolveSendModel picks the send model with the official picker's chain
// (prompt-model-selection.ts): current pending selection → session-adopted
// model → first connected provider's default ?? its first model. Every
// candidate still passes the catalog gate below — the fallback never escapes
// the connected catalog, so the legacy default-model failure mode (a made-up
// model id) cannot recur.
func (s *serverSession) resolveSendModel() (ocwModelRef, error) {
	if pending := s.a.GetModel(); pending != "" {
		providerID, modelID := parseQualifiedModel(pending)
		if modelID != "" {
			return ocwModelRef{ProviderID: providerID, ID: modelID}, nil
		}
	}
	if m, ok := s.model.Load().(*ocwModelRef); ok && m != nil && m.ID != "" {
		return *m, nil
	}
	catalog, err := s.a.fetchModelCatalog(s.ctx, s.client)
	if err != nil {
		return ocwModelRef{}, fmt.Errorf("opencode-web: no model selected and the catalog is unavailable: %w", err)
	}
	if ref, ok := catalog.fallbackModel(); ok {
		return ref, nil
	}
	return ocwModelRef{}, fmt.Errorf("opencode-web: no usable model — the server's connected provider catalog is empty (configure a provider in OpenCode first)")
}

// fallbackModel mirrors the official picker fallback: the FIRST connected
// provider's default model, else its first listed model.
func (c *ocwModelCatalog) fallbackModel() (ocwModelRef, bool) {
	for _, providerID := range c.connectedOrder {
		if modelID, ok := c.defaults[providerID]; ok && modelID != "" {
			return ocwModelRef{ProviderID: providerID, ID: modelID}, true
		}
		for _, m := range c.Models {
			if p, id := parseQualifiedModel(m.Name); p == providerID && id != "" {
				return ocwModelRef{ProviderID: p, ID: id}, true
			}
		}
	}
	return ocwModelRef{}, false
}

// Send implements core.AgentSession. Phase 1 is text-only: non-empty image or
// file attachments fail loudly instead of being silently dropped (design
// §4.3.4). The model is validated against the runtime catalog BEFORE any
// POST — a model outside the catalog is an immediate, diagnosable error.
func (s *serverSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is closed")
	}
	if len(images) > 0 || len(files) > 0 {
		return fmt.Errorf("opencode-web: image/file attachments are not supported in phase 1 (text only); attachment was rejected, not silently dropped")
	}

	model, err := s.resolveSendModel()
	if err != nil {
		return err
	}
	resolved, ok := s.a.modelInCatalog(s.ctx, s.client, model.ProviderID, model.ID)
	if !ok {
		qualified := model.ID
		if model.ProviderID != "" {
			qualified = model.ProviderID + "/" + model.ID
		}
		return fmt.Errorf("opencode-web: model %q is not in the server's provider catalog; refresh models and pick one from list_models", qualified)
	}
	s.model.Store(&resolved)

	chatID, err := s.ensureServerSession(resolved)
	if err != nil {
		return err
	}

	if s.client.Generation() == generationV2 {
		// v2: model rides the dedicated switch endpoint; prompt body carries
		// only the text (design §3.2 v2 column).
		if err := s.postModel(resolved, chatID); err != nil {
			return err
		}
		body := map[string]any{"prompt": prompt}
		code, raw, err := s.client.doRequest(s.ctx, http.MethodPost, s.client.endpoint(s.client.apiPath("/session/")+chatID+"/prompt"), body, s.directoryHeader(), true)
		if err != nil {
			return fmt.Errorf("opencode-web prompt: %w", err)
		}
		if code == 204 || code == 200 {
			return nil
		}
		return fmt.Errorf("opencode-web prompt HTTP %d: %s", code, truncateForError(string(raw)))
	}

	// Live-pinned on 1.18.18 (sandbox E2E 2026-08-19): prompt_async's model
	// object uses `modelID` (400 "Missing key at [model][modelID]" with `id`),
	// while POST /session create uses `id` — the two write routes differ.
	body := map[string]any{
		"parts": []map[string]any{{"type": "text", "text": prompt}},
		"model": map[string]any{"modelID": resolved.ID, "providerID": resolved.ProviderID},
	}
	code, raw, err := s.client.doRequest(s.ctx, http.MethodPost, s.client.endpoint("/session/"+chatID+"/prompt_async"), body, s.directoryHeader(), true)
	if err != nil {
		return fmt.Errorf("opencode-web prompt_async: %w", err)
	}
	if code == 204 || code == 200 {
		return nil
	}
	return fmt.Errorf("opencode-web prompt_async HTTP %d: %s", code, truncateForError(string(raw)))
}

func (s *serverSession) directoryHeader() string {
	if id := s.CurrentSessionID(); id != "" {
		// The session's own directory is the correct header once bound; fall
		// back to the agent work dir (the create case).
		if info, err := s.a.fetchSessionInfo(s.ctx, s.client, id); err == nil && info.Directory != "" {
			return info.Directory
		}
	}
	return s.a.GetWorkDir()
}

// ensureServerSession returns the serve-side session id, creating the session
// (with the catalog model) on first Send when no resume id was supplied, and
// locking the SSE filter to it.
func (s *serverSession) ensureServerSession(model ocwModelRef) (string, error) {
	if id := s.CurrentSessionID(); id != "" {
		return id, nil
	}
	dir := s.a.GetWorkDir()
	// Official v1 SDK shape (SessionCreateData): POST /session?directory=<dir>
	// with an OPTIONAL {parentID?, title?} body — model is NOT part of create;
	// the first prompt_async's body.model is what binds the session's model
	// (the old body {directory, model{id}} was tolerated by the serve as
	// extra keys but is not the official shape).
	body := map[string]any{}
	code, raw, err := s.client.doRequest(s.ctx, http.MethodPost, s.client.endpoint(s.client.apiPath("/session")), body, dir, true)
	if err != nil {
		return "", fmt.Errorf("opencode-web create session: %w", err)
	}
	if code >= 300 {
		return "", fmt.Errorf("opencode-web create session HTTP %d: %s", code, truncateForError(string(raw)))
	}
	var resp struct {
		ID  string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("opencode-web create session: bad response: %s", truncateForError(string(raw)))
	}
	created := resp.ID
	if created == "" {
		// v2 envelope: {"data": {...}} (V2SessionCreateResponses).
		created = resp.Data.ID
	}
	if created == "" {
		return "", fmt.Errorf("opencode-web create session: bad response: %s", truncateForError(string(raw)))
	}
	s.chatID.Store(created)
	s.sub.setSessionFilter(created)
	return created, nil
}

// postModel applies the v2 session-level model switch endpoint.
// Official v2 shape (V2SessionSwitchModelData): POST /api/session/{id}/model
// body {"model": ModelRef{id, providerID, variant?}} — nested, NOT flattened
// (the old flat {providerID, modelID} body was a shape drift, 2026-08-19 audit).
func (s *serverSession) postModel(model ocwModelRef, chatID string) error {
	body := map[string]any{"model": map[string]any{"id": model.ID, "providerID": model.ProviderID}}
	code, raw, err := s.client.doRequest(s.ctx, http.MethodPost, s.client.endpoint(s.client.apiPath("/session/")+chatID+"/model"), body, s.directoryHeader(), true)
	if err != nil {
		return fmt.Errorf("opencode-web switch model: %w", err)
	}
	if code >= 300 {
		return fmt.Errorf("opencode-web switch model HTTP %d: %s", code, truncateForError(string(raw)))
	}
	return nil
}

// CancelTurn implements core.TurnCanceler: abort (1.18) / interrupt (v2).
func (s *serverSession) CancelTurn(ctx context.Context) error {
	chatID := s.CurrentSessionID()
	if chatID == "" {
		return nil
	}
	path := "/session/" + chatID + "/abort"
	if s.client.Generation() == generationV2 {
		path = s.client.apiPath("/session/") + chatID + "/interrupt"
	}
	actx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Official shape: abort (v1 SessionAbortData) and interrupt (v2
	// V2SessionInterruptData) are both `body?: never` — no JSON body (the
	// former empty-object body was tolerated drift, 2026-08-19 audit).
	code, raw, err := s.client.doRequest(actx, http.MethodPost, s.client.endpoint(path), nil, s.directoryHeader(), true)
	if err != nil {
		return fmt.Errorf("opencode-web abort: %w", err)
	}
	if code >= 300 {
		return fmt.Errorf("opencode-web abort HTTP %d: %s", code, truncateForError(string(raw)))
	}
	return nil
}

// RespondPermission lands with the approvals phase (§8-6); until then it
// fails loudly instead of pretending.
func (s *serverSession) RespondPermission(requestID string, result core.PermissionResult) error {
	return s.a.respondPermission(s.ctx, s.client, s.CurrentSessionID(), requestID, result)
}

// Questions are ⛔ phase 1 (design §3.4/§4.3.4): the 1.18 reply path is not
// live-pinned, so the surface stays not_supported rather than fabricated.
func (s *serverSession) RespondQuestion(_ string, _ []string) error {
	return core.ErrNotSupported
}

func (s *serverSession) RejectQuestion(_ string) error {
	return core.ErrNotSupported
}

// Close tears down the SSE binding ONLY — it never aborts the running turn
// (explicit CancelTurn owns that) and never touches the serve process.
func (s *serverSession) Close() error {
	s.alive.Store(false)
	s.cancel() // → sub.Close() via the goroutine started in StartSession
	return nil
}

// GetContextUsage implements core.ContextUsageReporter from the last computed
// value (refreshed by the SSE session.updated recompute).
func (s *serverSession) GetContextUsage() *core.ContextUsage {
	return s.a.cachedContextUsage(s.CurrentSessionID())
}

var _ core.AgentSession = (*serverSession)(nil)
var _ core.TurnCanceler = (*serverSession)(nil)
var _ core.ContextUsageReporter = (*serverSession)(nil)
