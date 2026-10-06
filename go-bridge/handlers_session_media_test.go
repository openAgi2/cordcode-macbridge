package gobridge

// handlers_session_media_test.go — §5.4 handler-level tests (items 6/10/12
// plus the handler half of 8) for get_session_media: capability-absent fail
// close, stable media.* error passthrough with the official detail preserved
// (and the non-typed fail-closed default), the wire result shape (base64 +
// SHA round-trip), param validation, the workspace.read scope gate, and the
// session_media_read capability derivation.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// mediaCaptureConn records the single SendResult a handler test produces.
type mediaCaptureConn struct {
	data any
	err  *WireError
}

func (c *mediaCaptureConn) SendJSON(any)                               {}
func (c *mediaCaptureConn) SendResult(_ string, data any, err *WireError) {
	c.data, c.err = data, err
}
func (c *mediaCaptureConn) AuthedDevice() *TrustedDeviceRecord { return nil }
func (c *mediaCaptureConn) RemoteAddr() string                 { return "test:media" }
func (c *mediaCaptureConn) Close() error                       { return nil }

// fakeMediaAgent implements core.Agent + core.SessionMediaReader +
// core.SessionMediaDimensionProber.
type fakeMediaAgent struct {
	media       *core.SessionMedia
	mediaErr    error
	probeResult map[string]*core.SessionMediaDimensions
	probeErr    error
	probeCalls  []string
	calls       []string
}

func (f *fakeMediaAgent) Name() string { return "fake-media" }
func (f *fakeMediaAgent) StartSession(context.Context, string) (core.AgentSession, error) {
	return &fakeAgentSession{id: "media", events: make(chan core.Event)}, nil
}
func (f *fakeMediaAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (f *fakeMediaAgent) Stop() error { return nil }
func (f *fakeMediaAgent) ReadSessionMedia(_ context.Context, sessionID, path string) (*core.SessionMedia, error) {
	f.calls = append(f.calls, sessionID+"|"+path)
	return f.media, f.mediaErr
}
func (f *fakeMediaAgent) ProbeSessionMediaDimensions(_ context.Context, sessionID string, paths []string) (map[string]*core.SessionMediaDimensions, error) {
	f.probeCalls = append(f.probeCalls, sessionID+"|"+strconv.Itoa(len(paths)))
	return f.probeResult, f.probeErr
}

func mediaRPC(params string) WireMessage {
	return WireMessage{
		RequestID: "r-media",
		Method:    "get_session_media",
		Params:    json.RawMessage(params),
	}
}

func TestGetSessionMediaWireResultShape(t *testing.T) {
	h := newTestHandlers(t)
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	sum := sha256.Sum256(png)
	agent := &fakeMediaAgent{media: &core.SessionMedia{
		ResolvedPath:  "/w/assets/a.png",
		MediaType:     "image/png",
		Data:          png,
		ContentSha256: hex.EncodeToString(sum[:]),
	}}
	conn := &mediaCaptureConn{}
	h.handleGetSessionMedia(conn, mediaRPC(`{"sessionId":"sess-1","path":"assets/a.png"}`), agent)
	if conn.err != nil {
		t.Fatalf("err = %v", conn.err)
	}
	res, ok := conn.data.(map[string]any)
	if !ok {
		t.Fatalf("result type %T, want map", conn.data)
	}
	if res["resolvedPath"] != "/w/assets/a.png" || res["mediaType"] != "image/png" || res["bytes"] != len(png) {
		t.Fatalf("result = %#v", res)
	}
	if res["contentSha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("contentSha256 = %v", res["contentSha256"])
	}
	data, err := base64.StdEncoding.DecodeString(res["data"].(string))
	if err != nil || string(data) != string(png) {
		t.Fatalf("base64 round-trip failed: %v", err)
	}
	if len(agent.calls) != 1 || agent.calls[0] != "sess-1|assets/a.png" {
		t.Fatalf("driver calls = %v", agent.calls)
	}
}

func TestGetSessionMediaBackendNotSupported(t *testing.T) {
	h := newTestHandlers(t)
	// fakeAgent does NOT implement core.SessionMediaReader — the RPC must
	// fail closed before any driver work (§5.4 test 10).
	agent := &fakeAgent{name: "no-media"}
	conn := &mediaCaptureConn{}
	h.handleGetSessionMedia(conn, mediaRPC(`{"sessionId":"s","path":"a.png"}`), agent)
	if conn.err == nil || conn.err.Code != core.SessionMediaBackendNotSupported {
		t.Fatalf("err = %v, want media.backend_not_supported", conn.err)
	}
	if conn.data != nil {
		t.Fatalf("fail-closed must not return data, got %#v", conn.data)
	}
}

func TestGetSessionMediaErrorPassthrough(t *testing.T) {
	h := newTestHandlers(t)
	for _, code := range []string{
		core.SessionMediaInvalidParams,
		core.SessionMediaSessionNotFound,
		core.SessionMediaPathEscape,
		core.SessionMediaNotFound,
		core.SessionMediaNotRegularFile,
		core.SessionMediaPermissionDenied,
		core.SessionMediaTooLarge,
		core.SessionMediaUnsupportedMediaType,
		core.SessionMediaTransportFailed,
	} {
		agent := &fakeMediaAgent{mediaErr: &core.SessionMediaError{Code: code, Detail: "HTTP 403: FS_PERMISSION_DENIED"}}
		conn := &mediaCaptureConn{}
		h.handleGetSessionMedia(conn, mediaRPC(`{"sessionId":"s","path":"a.png"}`), agent)
		if conn.err == nil || conn.err.Code != code {
			t.Fatalf("code %s: err = %v", code, conn.err)
		}
		if !strings.Contains(conn.err.Message, "FS_PERMISSION_DENIED") {
			t.Fatalf("official detail must survive: %q", conn.err.Message)
		}
	}

	// Non-typed driver error fails closed to media.transport_failed.
	agent := &fakeMediaAgent{mediaErr: errors.New("boom")}
	conn := &mediaCaptureConn{}
	h.handleGetSessionMedia(conn, mediaRPC(`{"sessionId":"s","path":"a.png"}`), agent)
	if conn.err == nil || conn.err.Code != core.SessionMediaTransportFailed {
		t.Fatalf("non-typed err = %v, want media.transport_failed", conn.err)
	}
}

func TestGetSessionMediaParamValidation(t *testing.T) {
	h := newTestHandlers(t)
	agent := &fakeMediaAgent{}
	for _, params := range []string{
		`{}`,
		`{"sessionId":"","path":"a.png"}`,
		`{"sessionId":"s","path":""}`,
	} {
		conn := &mediaCaptureConn{}
		h.handleGetSessionMedia(conn, mediaRPC(params), agent)
		if conn.err == nil || conn.err.Code != core.SessionMediaInvalidParams {
			t.Fatalf("params %s: err = %v, want media.invalid_params", params, conn.err)
		}
	}
	if len(agent.calls) != 0 {
		t.Fatalf("invalid params must not reach the driver, saw %v", agent.calls)
	}
}

func TestGetSessionMediaScopeGate(t *testing.T) {
	// §5.4 test 12: get_session_media is workspace.read — a session.read-only
	// paired device is denied at the AuthorizeRPC funnel before dispatch.
	policy := NewCapabilityPolicy()
	readOnly := &scopeTestConn{device: &TrustedDeviceRecord{GrantedScopes: []string{ScopeSessionRead}}}
	err := policy.AuthorizeRPC(readOnly, WireMessage{Method: "get_session_media"})
	if err == nil || err.Code != "forbidden" {
		t.Fatalf("session.read-only device must be forbidden, got %v", err)
	}
	full := &scopeTestConn{device: &TrustedDeviceRecord{}}
	if err := policy.AuthorizeRPC(full, WireMessage{Method: "get_session_media"}); err != nil {
		t.Fatalf("default-grant device must pass, got %v", err)
	}
}

func TestSessionMediaReadCapabilityDerivation(t *testing.T) {
	with := deriveBackendCapabilities("dsh-web", &fakeMediaAgent{}, "")
	if !sessionMediaCapsContain(with, "session_media_read") {
		t.Fatalf("SessionMediaReader backend must advertise session_media_read, got %v", with)
	}
	without := deriveBackendCapabilities("dsh-web", &fakeAgent{name: "x"}, "")
	if sessionMediaCapsContain(without, "session_media_read") {
		t.Fatalf("non-implementing backend must NOT advertise session_media_read, got %v", without)
	}
}

func sessionMediaCapsContain(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

func mediaDimensionsRPC(params string) WireMessage {
	return WireMessage{
		RequestID: "r-media-dims",
		Method:    "get_session_media_dimensions",
		Params:    json.RawMessage(params),
	}
}

func TestGetSessionMediaDimensionsWireResultShape(t *testing.T) {
	h := newTestHandlers(t)
	agent := &fakeMediaAgent{probeResult: map[string]*core.SessionMediaDimensions{
		"a.png": {MediaType: "image/png", Width: 720, Height: 420},
		"b.svg": {MediaType: "image/svg+xml", Width: 100, Height: 50},
	}}
	conn := &mediaCaptureConn{}
	h.handleGetSessionMediaDimensions(conn, mediaDimensionsRPC(`{"sessionId":"sess-1","paths":["a.png","b.svg"]}`), agent)
	if conn.err != nil {
		t.Fatalf("err = %v", conn.err)
	}
	res, ok := conn.data.(map[string]any)
	if !ok {
		t.Fatalf("result type %T, want map", conn.data)
	}
	dims, ok := res["dimensions"].(map[string]map[string]any)
	if !ok {
		t.Fatalf("dimensions type %T, want map", res["dimensions"])
	}
	png, ok := dims["a.png"]
	if !ok || png["mediaType"] != "image/png" || png["width"] != 720 || png["height"] != 420 {
		t.Fatalf("a.png entry = %#v", dims["a.png"])
	}
	svg, ok := dims["b.svg"]
	if !ok || svg["mediaType"] != "image/svg+xml" || svg["width"] != 100 || svg["height"] != 50 {
		t.Fatalf("b.svg entry = %#v", dims["b.svg"])
	}
	if len(agent.probeCalls) != 1 || agent.probeCalls[0] != "sess-1|2" {
		t.Fatalf("driver probe calls = %v", agent.probeCalls)
	}
}

func TestGetSessionMediaDimensionsBackendNotSupported(t *testing.T) {
	h := newTestHandlers(t)
	// fakeAgent implements neither media interface — the probe must fail
	// closed before any driver work.
	agent := &fakeAgent{name: "no-media-dims"}
	conn := &mediaCaptureConn{}
	h.handleGetSessionMediaDimensions(conn, mediaDimensionsRPC(`{"sessionId":"s","paths":["a.png"]}`), agent)
	if conn.err == nil || conn.err.Code != core.SessionMediaBackendNotSupported {
		t.Fatalf("err = %v, want media.backend_not_supported", conn.err)
	}
	if conn.data != nil {
		t.Fatalf("fail-closed must not return data, got %#v", conn.data)
	}
}

func TestGetSessionMediaDimensionsParamValidation(t *testing.T) {
	h := newTestHandlers(t)
	agent := &fakeMediaAgent{}
	manyPaths := make([]string, sessionMediaDimensionsMaxPaths+1)
	for i := range manyPaths {
		manyPaths[i] = "a.png"
	}
	manyJSON, _ := json.Marshal(map[string]any{"sessionId": "s", "paths": manyPaths})
	for _, params := range []string{
		`{}`,
		`{"sessionId":"","paths":["a.png"]}`,
		`{"sessionId":"s","paths":[]}`,
		string(manyJSON),
	} {
		conn := &mediaCaptureConn{}
		h.handleGetSessionMediaDimensions(conn, mediaDimensionsRPC(params), agent)
		if conn.err == nil || conn.err.Code != core.SessionMediaInvalidParams {
			t.Fatalf("params %s: err = %v, want media.invalid_params", params, conn.err)
		}
	}
	if len(agent.probeCalls) != 0 {
		t.Fatalf("invalid params must not reach the driver, saw %v", agent.probeCalls)
	}
}

func TestGetSessionMediaDimensionsErrorPassthrough(t *testing.T) {
	h := newTestHandlers(t)
	agent := &fakeMediaAgent{probeErr: &core.SessionMediaError{Code: core.SessionMediaSessionNotFound, Detail: "session not present in session/list"}}
	conn := &mediaCaptureConn{}
	h.handleGetSessionMediaDimensions(conn, mediaDimensionsRPC(`{"sessionId":"s","paths":["a.png"]}`), agent)
	if conn.err == nil || conn.err.Code != core.SessionMediaSessionNotFound {
		t.Fatalf("typed err = %v, want media.session_not_found", conn.err)
	}

	agent = &fakeMediaAgent{probeErr: errors.New("boom")}
	conn = &mediaCaptureConn{}
	h.handleGetSessionMediaDimensions(conn, mediaDimensionsRPC(`{"sessionId":"s","paths":["a.png"]}`), agent)
	if conn.err == nil || conn.err.Code != core.SessionMediaTransportFailed {
		t.Fatalf("non-typed err = %v, want media.transport_failed", conn.err)
	}
}

func TestGetSessionMediaDimensionsScopeGate(t *testing.T) {
	// Same media read surface: workspace.read — a session.read-only paired
	// device is denied at the AuthorizeRPC funnel before dispatch.
	policy := NewCapabilityPolicy()
	readOnly := &scopeTestConn{device: &TrustedDeviceRecord{GrantedScopes: []string{ScopeSessionRead}}}
	err := policy.AuthorizeRPC(readOnly, WireMessage{Method: "get_session_media_dimensions"})
	if err == nil || err.Code != "forbidden" {
		t.Fatalf("session.read-only device must be forbidden, got %v", err)
	}
	full := &scopeTestConn{device: &TrustedDeviceRecord{}}
	if err := policy.AuthorizeRPC(full, WireMessage{Method: "get_session_media_dimensions"}); err != nil {
		t.Fatalf("default-grant device must pass, got %v", err)
	}
}

func TestSessionMediaDimensionsCapabilityDerivation(t *testing.T) {
	with := deriveBackendCapabilities("dsh-web", &fakeMediaAgent{}, "")
	if !sessionMediaCapsContain(with, "session_media_dimensions") {
		t.Fatalf("prober backend must advertise session_media_dimensions, got %v", with)
	}
	without := deriveBackendCapabilities("dsh-web", &fakeAgent{name: "x"}, "")
	if sessionMediaCapsContain(without, "session_media_dimensions") {
		t.Fatalf("non-implementing backend must NOT advertise session_media_dimensions, got %v", without)
	}
}
