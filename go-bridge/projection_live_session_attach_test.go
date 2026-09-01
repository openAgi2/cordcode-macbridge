package gobridge

import (
	"context"
	"sync"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type projectionAttachSession struct {
	id     string
	events chan core.Event
	mu     sync.Mutex
	alive  bool
}

func (s *projectionAttachSession) Send(string, []core.ImageAttachment, []core.FileAttachment) error {
	return nil
}
func (s *projectionAttachSession) RespondPermission(string, core.PermissionResult) error {
	return core.ErrNotSupported
}
func (s *projectionAttachSession) Events() <-chan core.Event { return s.events }
func (s *projectionAttachSession) CurrentSessionID() string  { return s.id }
func (s *projectionAttachSession) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive
}
func (s *projectionAttachSession) Close() error {
	s.mu.Lock()
	s.alive = false
	s.mu.Unlock()
	return nil
}
func (s *projectionAttachSession) RespondQuestion(string, []string) error {
	return core.ErrNotSupported
}
func (s *projectionAttachSession) RejectQuestion(string) error { return core.ErrNotSupported }

type projectionAttachAgent struct {
	mu       sync.Mutex
	attaches int
	session  *projectionAttachSession
}

func (a *projectionAttachAgent) Name() string { return "codex-remote" }
func (a *projectionAttachAgent) StartSession(context.Context, string) (core.AgentSession, error) {
	return a.session, nil
}
func (a *projectionAttachAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (a *projectionAttachAgent) Stop() error { return nil }
func (a *projectionAttachAgent) AttachProjectionLiveSession(_ context.Context, threadID string) (core.AgentSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attaches++
	if a.session == nil {
		a.session = &projectionAttachSession{id: threadID, events: make(chan core.Event), alive: true}
	}
	return a.session, nil
}

func TestProjectionOpenAttachesLiveSessionBeforeFirstSend(t *testing.T) {
	h := NewHandlers()
	agent := &projectionAttachAgent{}
	conn := newCaptureConn()
	h.startProjectionLiveRelay("thread-open-only", conn, "codex-remote", agent, "/tmp/project")

	agent.mu.Lock()
	attaches := agent.attaches
	agent.mu.Unlock()
	if attaches != 1 {
		t.Fatalf("projection open attaches=%d, want 1", attaches)
	}
	h.mu.Lock()
	sess, ok := h.getSession("thread-open-only")
	running := h.agentRelayRunning["thread-open-only"]
	h.mu.Unlock()
	if !ok || sess == nil || !running {
		t.Fatalf("projection open did not register/start live relay: ok=%v session=%v running=%v", ok, sess, running)
	}

	// A warm projection pull must reuse the same listener rather than resume a
	// second writer or replace the relay.
	h.startProjectionLiveRelay("thread-open-only", conn, "codex-remote", agent, "/tmp/project")
	agent.mu.Lock()
	attaches = agent.attaches
	agent.mu.Unlock()
	if attaches != 1 {
		t.Fatalf("warm projection open attaches=%d, want 1", attaches)
	}
	_ = sess.Close()
}

var _ core.ProjectionLiveSessionAttacher = (*projectionAttachAgent)(nil)
