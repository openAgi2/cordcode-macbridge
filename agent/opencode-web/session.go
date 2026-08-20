package opencodeweb

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// Send implements core.AgentSession by delegating to SendWithOptions with no
// per-request options (the backend resolves agent/model/variant itself).
func (s *serverSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	return s.SendWithOptions(prompt, images, files, core.PromptOptions{})
}

// newMessageID generates the Mac-side stable OpenCode message id, exactly
// once per prompt (canonical §6.4/§6.11.1 item 6). It is correlation-only:
// the id rides the request and matches the persisted user message; it never
// makes iOS a timeline writer.
func newMessageID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&messageIDSeq, 1))
	}
	return "msg_" + hex.EncodeToString(b[:])
}

var messageIDSeq uint64

// attachmentParts maps existing bridge attachments onto the official prompt
// file part (§6.4 verified transport shape): both images and files become
// {type:"file", mime, filename?, url:"data:<mime>;base64,<base64>"}.
func attachmentParts(images []core.ImageAttachment, files []core.FileAttachment) []map[string]any {
	parts := make([]map[string]any, 0, len(images)+len(files))
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		mime := img.MimeType
		if mime == "" {
			mime = "application/octet-stream"
		}
		part := map[string]any{
			"type": "file",
			"mime": mime,
			"url":  "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
		}
		if img.FileName != "" {
			part["filename"] = img.FileName
		}
		parts = append(parts, part)
	}
	for _, f := range files {
		if len(f.Data) == 0 {
			continue
		}
		mime := f.MimeType
		if mime == "" {
			mime = "application/octet-stream"
		}
		part := map[string]any{
			"type": "file",
			"mime": mime,
			"url":  "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(f.Data),
		}
		if f.FileName != "" {
			part["filename"] = f.FileName
		}
		parts = append(parts, part)
	}
	return parts
}

// SendWithOptions implements core.PromptOptionsSender (canonical §6.4/§6.11.1):
// one atomic prompt carrying the Mac-generated-once stable messageID, the
// resolved agent, the §6.6-resolved validated model, an optional live variant
// key, and the supported parts. Unsupported inputs (unlisted variant,
// unavailable agent/model) fail BEFORE any POST — zero network I/O. A 204
// answer is ADMISSION ONLY: no timeline write, no synthesized message.
func (s *serverSession) SendWithOptions(prompt string, images []core.ImageAttachment, files []core.FileAttachment, opts core.PromptOptions) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is closed")
	}
	if strings.TrimSpace(prompt) == "" && len(images) == 0 && len(files) == 0 {
		return fmt.Errorf("opencode-web: prompt is empty (no text and no attachments)")
	}

	agentID, agentModel, err := s.a.resolvePromptAgent(s.ctx, s.client, opts.Agent)
	if err != nil {
		return err
	}
	explicit := ocwModelRef{ProviderID: opts.ProviderID, ID: opts.ModelID}
	resolved, err := s.resolvePromptModel(s.ctx, s.client, explicit, agentModel)
	if err != nil {
		return err
	}
	s.model.Store(&resolved)

	if opts.Variant != "" {
		live := s.a.modelVariants(s.ctx, s.client, resolved.ProviderID, resolved.ID)
		found := false
		for _, key := range live {
			if key == opts.Variant {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("opencode-web: variant %q is not a live key of model %s/%s (live keys: %v) — zero POSTs", opts.Variant, resolved.ProviderID, resolved.ID, live)
		}
	}

	chatID, err := s.ensureServerSession(resolved)
	if err != nil {
		return err
	}

	parts := []map[string]any{{"type": "text", "text": prompt}}
	parts = append(parts, attachmentParts(images, files)...)
	body := map[string]any{
		"messageID": newMessageID(),
		"agent":     agentID,
		"model":     map[string]any{"providerID": resolved.ProviderID, "modelID": resolved.ID},
		"parts":     parts,
	}
	if opts.Variant != "" {
		body["variant"] = opts.Variant
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
		ID   string `json:"id"`
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

// CancelTurn implements core.TurnCanceler. C1 quarantines unverified
// generations at clientFor, so only the verified 1.18.18 abort route exists —
// the former v2 /interrupt product path is deleted, not merely unreachable.
func (s *serverSession) CancelTurn(ctx context.Context) error {
	chatID := s.CurrentSessionID()
	if chatID == "" {
		return nil
	}
	path := "/session/" + chatID + "/abort"
	actx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Official shape: abort (v1 SessionAbortData) is `body?: never` — no JSON
	// body (the former empty-object body was tolerated drift, 2026-08-19 audit).
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
var _ core.PromptOptionsSender = (*serverSession)(nil)
var _ core.TurnCanceler = (*serverSession)(nil)
var _ core.ContextUsageReporter = (*serverSession)(nil)
