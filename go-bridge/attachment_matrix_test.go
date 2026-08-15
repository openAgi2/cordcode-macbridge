package gobridge

// §16 gate 1: attachment rejection matrix — every rejection asserted
// pre-StartSession. §16 gate 2 (mechanism half): deriveBackendCapabilities
// advertises the positively declared attachment kinds from the same
// AttachmentSupporter source the gate reads. Driver truth-half lives in each
// agent package's own test (they assert the real kind sets).

import (
	"context"
	"errors"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var errNoScriptedSession = errors.New("no scripted sessions")

// matrixAgent stubs a backend with a fixed positive declaration.
type matrixAgent struct {
	name     string
	kinds    []string
	starts   int
	sessions []*matrixSession
}

func (a *matrixAgent) Name() string { return a.name }
func (a *matrixAgent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.starts++
	if len(a.sessions) == 0 {
		return nil, errNoScriptedSession
	}
	s := a.sessions[0]
	a.sessions = a.sessions[1:]
	return s, nil
}
func (a *matrixAgent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	return nil, core.ErrNotSupported
}
func (a *matrixAgent) Stop() error                        { return nil }
func (a *matrixAgent) SupportedAttachmentKinds() []string { return a.kinds }

type matrixSession struct {
	events  chan core.Event
	images  int
	files   int
	sendErr error
	alive   bool
	closed  bool
}

func (s *matrixSession) Send(_ string, images []core.ImageAttachment, files []core.FileAttachment) error {
	s.images += len(images)
	s.files += len(files)
	return s.sendErr
}
func (s *matrixSession) RespondPermission(string, core.PermissionResult) error { return nil }
func (s *matrixSession) Events() <-chan core.Event                             { return s.events }
func (s *matrixSession) CurrentSessionID() string                              { return "ses-matrix" }
func (s *matrixSession) Alive() bool                                           { return s.alive }
func (s *matrixSession) RespondQuestion(string, []string) error                { return nil }
func (s *matrixSession) RejectQuestion(string) error                           { return nil }
func (s *matrixSession) Close() error {
	s.closed = true
	close(s.events)
	return nil
}

var matrixBackends = map[string][]string{
	// mirrors the real drivers' AttachmentSupporter declarations (§3.9 matrix):
	"claude-like":    {"image", "file"}, // claudecode / codex
	"oc-cli-like":    {"image", "file"}, // opencode without managed server
	"oc-server-like": {"file"},          // opencode managed-server mode
	"grok-like":      {"file"},          // grokbuild
	"dsh-like":       nil,               // dsh: text-only, interface not implemented → nil kinds
}

func sendWithAttachment(t *testing.T, backend string, attachments string) (map[string]any, *matrixAgent, *matrixSession) {
	t.Helper()
	agent := &matrixAgent{name: backend, kinds: matrixBackends[backend]}
	if agent.kinds == nil && backend == "dsh-like" {
		// dsh does not implement AttachmentSupporter at all.
		agent = &matrixAgent{name: backend}
	}
	sess := &matrixSession{events: make(chan core.Event, 8), alive: true}
	agent.sessions = []*matrixSession{sess}

	h := newTestHandlers(t)
	h.RegisterAgent(backend, agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleSendMessage(serverConn, WireMessage{
		BackendID: backend, Method: "send_message", RequestID: "r1",
		Params: []byte(`{"sessionId":"ses-matrix","content":"hi","attachments":[` + attachments + `]}`),
	}, agent)
	// Rejected sends produce exactly one frame (the error result, no running
	// broadcast — the pre-check runs before any side effect); accepted sends
	// produce the running broadcast first. Read adaptively.
	var result map[string]any
	for read := 0; read < 2 && result == nil; read++ {
		frames := readJSONMaps(t, clientConn, 1)
		if frames[0]["type"] == "result" {
			result = frames[0]
		}
	}
	if result == nil {
		t.Fatal("no result frame received")
	}
	return result, agent, sess
}

func resultErrCode(t *testing.T, result map[string]any) string {
	t.Helper()
	errMap, ok := result["error"].(map[string]any)
	if !ok {
		return ""
	}
	code, _ := errMap["code"].(string)
	return code
}

// Valid file: accepted by every file-capable backend; rejected by DSH.
func TestAttachmentMatrixValidFile(t *testing.T) {
	fileJSON := `{"kind":"file","mime":"application/pdf","base64":"JVBERg=="}`

	for _, backend := range []string{"claude-like", "oc-cli-like", "oc-server-like", "grok-like"} {
		result, agent, sess := sendWithAttachment(t, backend, fileJSON)
		if code := resultErrCode(t, result); code != "" {
			t.Fatalf("%s: valid file rejected: %s", backend, code)
		}
		if agent.starts != 1 || sess.files != 1 || sess.images != 0 {
			t.Fatalf("%s: file must reach Send (starts=%d files=%d)", backend, agent.starts, sess.files)
		}
	}

	result, dshAgent, dshSess := sendWithAttachment(t, "dsh-like", fileJSON)
	if code := resultErrCode(t, result); code != "unsupported_attachment" {
		t.Fatalf("dsh: want unsupported_attachment, got %q", code)
	}
	if dshAgent.starts != 0 || dshSess.files != 0 {
		t.Fatal("dsh: rejection must happen pre-StartSession with zero sends")
	}
}

// Valid image: accepted by claude/codex/OC-CLI; rejected by OC-server
// (mode-aware: the server path silently drops images), grokbuild
// (promptCapabilities.image=false), DSH.
func TestAttachmentMatrixValidImage(t *testing.T) {
	imgJSON := `{"kind":"image","mime":"image/png","base64":"iVBORw=="}`

	for _, backend := range []string{"claude-like", "oc-cli-like"} {
		result, _, sess := sendWithAttachment(t, backend, imgJSON)
		if code := resultErrCode(t, result); code != "" {
			t.Fatalf("%s: valid image rejected: %s", backend, code)
		}
		if sess.images != 1 || sess.files != 0 {
			t.Fatalf("%s: image must reach Send via image path", backend)
		}
	}
	for _, backend := range []string{"oc-server-like", "grok-like", "dsh-like"} {
		result, agent, _ := sendWithAttachment(t, backend, imgJSON)
		if code := resultErrCode(t, result); code != "unsupported_attachment" {
			t.Fatalf("%s: want unsupported_attachment, got %q", backend, code)
		}
		if agent.starts != 0 {
			t.Fatalf("%s: rejection must be pre-StartSession", backend)
		}
	}
}

// round11 mismatch fixture (instantiated to a concrete subtype per round12):
// raw kind=file with image/* mime has effectiveKind=image — image-capable
// backends take the IMAGE path, file-only/none backends reject.
func TestAttachmentMatrixKindMimeMismatch(t *testing.T) {
	mismatchJSON := `{"kind":"file","mime":"image/png","base64":"iVBORw=="}`

	for _, backend := range []string{"claude-like", "oc-cli-like"} {
		result, _, sess := sendWithAttachment(t, backend, mismatchJSON)
		if code := resultErrCode(t, result); code != "" {
			t.Fatalf("%s: effectiveKind=image must pass image gate, got %q", backend, code)
		}
		if sess.images != 1 || sess.files != 0 {
			t.Fatalf("%s: mismatch fixture must route through the IMAGE path (images=%d files=%d)", backend, sess.images, sess.files)
		}
	}
	for _, backend := range []string{"oc-server-like", "grok-like", "dsh-like"} {
		result, agent, _ := sendWithAttachment(t, backend, mismatchJSON)
		if code := resultErrCode(t, result); code != "unsupported_attachment" {
			t.Fatalf("%s: mismatch fixture must hit the image gate, got %q", backend, code)
		}
		if agent.starts != 0 {
			t.Fatalf("%s: rejection must be pre-StartSession", backend)
		}
	}
}

// Malformed MIME — three frozen cases — and structural garbage: invalid_params
// for EVERY backend, whole message rejected pre-StartSession.
func TestAttachmentMatrixRawStructureRejection(t *testing.T) {
	structural := []struct {
		name string
		json string
	}{
		{"mime not-a-mime", `{"kind":"file","mime":"not-a-mime","base64":"iVBORw=="}`},
		{"mime bare type", `{"kind":"file","mime":"image","base64":"iVBORw=="}`},
		{"mime with parameter", `{"kind":"file","mime":"image/png; charset=utf-8","base64":"iVBORw=="}`},
		{"mime wildcard literal", `{"kind":"file","mime":"image/*","base64":"iVBORw=="}`},
		{"empty kind", `{"kind":"","mime":"text/plain","base64":"iVBORw=="}`},
		{"invalid base64", `{"kind":"file","mime":"application/pdf","base64":"@@bad@@"}`},
		{"empty base64", `{"kind":"file","mime":"application/pdf","base64":""}`},
		{"mixed valid+invalid", `{"kind":"image","mime":"image/png","base64":"iVBORw=="},{"kind":"file","mime":"not-a-mime","base64":"iVBORw=="}`},
	}
	backends := []string{"claude-like", "oc-cli-like", "oc-server-like", "grok-like", "dsh-like"}
	for _, tc := range structural {
		for _, backend := range backends {
			result, agent, sess := sendWithAttachment(t, backend, tc.json)
			if code := resultErrCode(t, result); code != "invalid_params" {
				t.Fatalf("%s / %s: want invalid_params, got %q", tc.name, backend, code)
			}
			if agent.starts != 0 || sess.files != 0 || sess.images != 0 {
				t.Fatalf("%s / %s: structural rejection must be pre-StartSession with zero sends", tc.name, backend)
			}
		}
	}
}

// §16 gate 2 (mechanism half): the advertised capability set derives from the
// SAME AttachmentSupporter declaration the gate reads.
func TestDeriveCapabilitiesIncludesAttachmentKinds(t *testing.T) {
	agent := &matrixAgent{name: "claude-like", kinds: []string{"image", "file"}}
	caps := deriveBackendCapabilities("claude-like", agent, "")
	has := func(want string) bool {
		for _, c := range caps {
			if c == want {
				return true
			}
		}
		return false
	}
	if !has("image") || !has("file") {
		t.Fatalf("advertised caps must include image+file: %v", caps)
	}

	none := &matrixAgent{name: "dsh-like"} // no AttachmentSupporter
	caps2 := deriveBackendCapabilities("dsh-like", none, "")
	for _, c := range caps2 {
		if c == "image" || c == "file" {
			t.Fatalf("undeclared kinds must not be advertised: %v", caps2)
		}
	}
}
