package gobridge

// Management v1 observed fixtures are captured from the real authenticated HTTP handlers.
// The clock, identity, operation IDs and token are deterministic non-production values; no
// machine identity or live management secret can enter the committed samples.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
)

const (
	fixtureOperationA = "ffeeddccbbaa99887766554433221100"
	fixtureOperationB = "112233445566778899aabbccddeeff00"
	fixtureTokenA     = "00112233445566778899aabbccddeeff"
	fixtureTokenB     = "99887766554433221100ffeeddccbbaa"
)

type managementFixtureClock struct{ now time.Time }

func newManagementV1FixtureServer(t *testing.T) (*ManagementServer, *managementFixtureClock) {
	t.Helper()
	started, _ := v0FixtureClock()
	clock := &managementFixtureClock{now: started.Add(time.Hour)}
	cfg := v0FixtureConfig()
	cfg.RuntimeIdentity = admission.RuntimeIdentity{PID: 12345, BridgeEpoch: 1}
	srv := NewManagementServer(cfg)
	srv.startedAt = started
	srv.now = func() time.Time { return clock.now }
	srv.admission = admission.NewAdmissionMachine(cfg.RuntimeIdentity, func() time.Time { return clock.now }, 30_000)
	token, err := admission.DecodeToken(fixtureTokenA)
	if err != nil {
		t.Fatal(err)
	}
	srv.admission.SetTokenGenerator(func() (admission.Token, error) { return token, nil })
	srv.cfg.Handlers.SetAdmissionMachine(srv.admission)
	return srv, clock
}

func managementFixtureQuiesceBody(operation string, pid int, health uint64) string {
	return fmt.Sprintf(`{"managementSchemaVersion":1,"operationId":"%s","expectedRuntime":{"pid":%d,"bridgeEpoch":1},"expectedHealthEpoch":%d}`, operation, pid, health)
}

func managementFixtureCommitBody(operation string, pid int, health, quiesce uint64, token string) string {
	return fmt.Sprintf(`{"managementSchemaVersion":1,"operationId":"%s","expectedRuntime":{"pid":%d,"bridgeEpoch":1},"expectedHealthEpoch":%d,"quiesceEpoch":%d,"token":"%s"}`, operation, pid, health, quiesce, token)
}

func captureManagementFixture(t *testing.T, srv *ManagementServer, method, path, body string) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = authRequest(method, path)
	} else {
		req = authJSONRequest(method, path, body)
	}
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
	}
	return bytes.Clone(rec.Body.Bytes())
}

func fixtureQuiesceDirect(t *testing.T, srv *ManagementServer, operation string) (admission.OperationID, admission.Token) {
	t.Helper()
	op, err := admission.DecodeOperationID(operation)
	if err != nil {
		t.Fatal(err)
	}
	result := srv.admission.Quiesce(admission.QuiesceRequest{
		ManagementSchemaVersion: 1, OperationID: op,
		ExpectedRuntime: admission.RuntimeIdentity{PID: 12345, BridgeEpoch: 1}, ExpectedHealthEpoch: 1,
	})
	if result.Outcome != admission.QuiesceSafe {
		t.Fatalf("fixture setup quiesce=%s", result.Outcome)
	}
	return op, result.Token
}

func managementV1ObservedFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	fixtures := make(map[string][]byte)
	encode := func(value interface{}) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	fixtures["error-runtime-quiescing.json"] = encode(resultEnvelope("req-0001-test", nil, &WireError{
		Code: "runtime.quiescing", Message: "Bridge runtime is quiescing",
	}))
	fixtures["error-file-read-degraded.json"] = encode(resultEnvelope("req-0002-test", nil, &WireError{
		Code: "file.read_degraded", Message: "file read service is degraded; runtime restart required",
	}))

	// Level-triggered status: accepting -> leased (1s elapsed) -> committed.
	srv, clock := newManagementV1FixtureServer(t)
	fixtures["v1-status-accepting.json"] = captureManagementFixture(t, srv, http.MethodGet, "/internal/status", "")
	op, token := fixtureQuiesceDirect(t, srv, fixtureOperationA)
	clock.now = clock.now.Add(time.Second)
	fixtures["v1-status-quiescing.json"] = captureManagementFixture(t, srv, http.MethodGet, "/internal/status", "")
	commit := admission.CommitRequest{
		ManagementSchemaVersion: 1, OperationID: op,
		ExpectedRuntime: admission.RuntimeIdentity{PID: 12345, BridgeEpoch: 1}, ExpectedHealthEpoch: 1,
		QuiesceEpoch: 1, Token: token,
	}
	if result := srv.admission.Commit(commit); result.Outcome != admission.OutcomeCommitted {
		t.Fatalf("fixture setup commit=%s", result.Outcome)
	}
	fixtures["v1-status-shuttingDown.json"] = captureManagementFixture(t, srv, http.MethodGet, "/internal/status", "")

	// FileReadHealth and RuntimeAdmission are orthogonal: capture every 3×3 legal state
	// combination through the real health/admission machines and status handler.
	for _, healthCase := range []struct {
		name  string
		steps int
	}{
		{name: "degrading", steps: 1},
		{name: "degraded", steps: 2},
	} {
		for _, admissionState := range []string{"accepting", "quiescing", "shuttingDown"} {
			s, c := newManagementV1FixtureServer(t)
			health := s.cfg.Handlers.filePool.Health()
			health.MarkDegrading()
			if healthCase.steps == 2 {
				health.MarkDegraded()
			}
			s.syncAdmissionInputs()
			if admissionState != "accepting" {
				op, _ := admission.DecodeOperationID(fixtureOperationA)
				result := s.admission.Quiesce(admission.QuiesceRequest{
					ManagementSchemaVersion: 1, OperationID: op,
					ExpectedRuntime:     admission.RuntimeIdentity{PID: 12345, BridgeEpoch: 1},
					ExpectedHealthEpoch: uint64(1 + healthCase.steps),
				})
				if result.Outcome != admission.QuiesceSafe {
					t.Fatalf("%s/%s setup quiesce=%s", healthCase.name, admissionState, result.Outcome)
				}
				if admissionState == "quiescing" {
					c.now = c.now.Add(time.Second)
				} else {
					commit := admission.CommitRequest{
						ManagementSchemaVersion: 1, OperationID: op,
						ExpectedRuntime:     admission.RuntimeIdentity{PID: 12345, BridgeEpoch: 1},
						ExpectedHealthEpoch: uint64(1 + healthCase.steps),
						QuiesceEpoch:        1, Token: result.Token,
					}
					if committed := s.admission.Commit(commit); committed.Outcome != admission.OutcomeCommitted {
						t.Fatalf("%s setup commit=%s", healthCase.name, committed.Outcome)
					}
				}
			}
			name := fmt.Sprintf("v1-status-%s-%s.json", healthCase.name, admissionState)
			fixtures[name] = captureManagementFixture(t, s, http.MethodGet, "/internal/status", "")
		}
	}

	quiesceCases := []struct {
		file, body string
		setup      func(*ManagementServer, *managementFixtureClock)
	}{
		{"quiesce-result-safe.json", managementFixtureQuiesceBody(fixtureOperationA, 12345, 1), nil},
		{"quiesce-result-deferred.json", managementFixtureQuiesceBody(fixtureOperationA, 12345, 1), func(s *ManagementServer, _ *managementFixtureClock) { s.admission.SetActivity(1, 0) }},
		{"quiesce-result-identity-mismatch.json", managementFixtureQuiesceBody(fixtureOperationA, 999, 1), nil},
		{"quiesce-result-epoch-mismatch.json", managementFixtureQuiesceBody(fixtureOperationA, 12345, 999), nil},
		{"quiesce-result-already-committed.json", managementFixtureQuiesceBody(fixtureOperationA, 12345, 1), func(s *ManagementServer, _ *managementFixtureClock) {
			op, token := fixtureQuiesceDirect(t, s, fixtureOperationA)
			_ = s.admission.Commit(admission.CommitRequest{ManagementSchemaVersion: 1, OperationID: op, ExpectedRuntime: admission.RuntimeIdentity{PID: 12345, BridgeEpoch: 1}, ExpectedHealthEpoch: 1, QuiesceEpoch: 1, Token: token})
		}},
		{"quiesce-result-already-quiescing.json", managementFixtureQuiesceBody(fixtureOperationB, 12345, 1), func(s *ManagementServer, _ *managementFixtureClock) { fixtureQuiesceDirect(t, s, fixtureOperationA) }},
		{"quiesce-result-operation-reused.json", managementFixtureQuiesceBody(fixtureOperationA, 12345, 1), func(s *ManagementServer, c *managementFixtureClock) {
			fixtureQuiesceDirect(t, s, fixtureOperationA)
			c.now = c.now.Add(30 * time.Second)
		}},
		{"quiesce-result-token-generation-failed.json", managementFixtureQuiesceBody(fixtureOperationA, 12345, 1), func(s *ManagementServer, _ *managementFixtureClock) {
			s.admission.SetTokenGenerator(func() (admission.Token, error) { return admission.Token{}, fmt.Errorf("fixture rng failure") })
		}},
	}
	for _, tc := range quiesceCases {
		s, c := newManagementV1FixtureServer(t)
		if tc.setup != nil {
			tc.setup(s, c)
		}
		fixtures[tc.file] = captureManagementFixture(t, s, http.MethodPost, "/internal/runtime/quiesce", tc.body)
	}

	type commitCase struct {
		file, endpoint, body string
		setup                func(*ManagementServer, *managementFixtureClock)
	}
	normal := managementFixtureCommitBody(fixtureOperationA, 12345, 1, 1, fixtureTokenA)
	identityMismatch := managementFixtureCommitBody(fixtureOperationA, 999, 1, 1, fixtureTokenA)
	epochMismatch := managementFixtureCommitBody(fixtureOperationA, 12345, 999, 1, fixtureTokenA)
	quiesceMismatch := managementFixtureCommitBody(fixtureOperationB, 12345, 1, 1, fixtureTokenA)
	tokenMismatch := managementFixtureCommitBody(fixtureOperationA, 12345, 1, 1, fixtureTokenB)
	active := func(s *ManagementServer, _ *managementFixtureClock) { fixtureQuiesceDirect(t, s, fixtureOperationA) }
	committed := func(s *ManagementServer, c *managementFixtureClock) {
		active(s, c)
		op, _ := admission.DecodeOperationID(fixtureOperationA)
		token, _ := admission.DecodeToken(fixtureTokenA)
		_ = s.admission.Commit(admission.CommitRequest{ManagementSchemaVersion: 1, OperationID: op, ExpectedRuntime: admission.RuntimeIdentity{PID: 12345, BridgeEpoch: 1}, ExpectedHealthEpoch: 1, QuiesceEpoch: 1, Token: token})
	}
	expired := func(s *ManagementServer, c *managementFixtureClock) {
		active(s, c)
		c.now = c.now.Add(30 * time.Second)
	}
	aborted := func(s *ManagementServer, c *managementFixtureClock) {
		active(s, c)
		op, _ := admission.DecodeOperationID(fixtureOperationA)
		token, _ := admission.DecodeToken(fixtureTokenA)
		_ = s.admission.Abort(admission.CommitRequest{ManagementSchemaVersion: 1, OperationID: op, ExpectedRuntime: admission.RuntimeIdentity{PID: 12345, BridgeEpoch: 1}, ExpectedHealthEpoch: 1, QuiesceEpoch: 1, Token: token})
	}
	cases := []commitCase{
		{"commit-result-committed.json", "/internal/runtime/commit-quiesced-shutdown", normal, active},
		{"commit-result-already-committed.json", "/internal/runtime/commit-quiesced-shutdown", normal, committed},
		{"commit-result-identity-mismatch.json", "/internal/runtime/commit-quiesced-shutdown", identityMismatch, active},
		{"commit-result-epoch-mismatch.json", "/internal/runtime/commit-quiesced-shutdown", epochMismatch, active},
		{"commit-result-quiesce-mismatch.json", "/internal/runtime/commit-quiesced-shutdown", quiesceMismatch, active},
		{"commit-result-token-mismatch.json", "/internal/runtime/commit-quiesced-shutdown", tokenMismatch, active},
		{"commit-result-lease-expired.json", "/internal/runtime/commit-quiesced-shutdown", normal, expired},
		{"abort-result-aborted.json", "/internal/runtime/abort-quiesce", normal, active},
		{"abort-result-already-accepting.json", "/internal/runtime/abort-quiesce", normal, aborted},
		{"abort-result-already-committed.json", "/internal/runtime/abort-quiesce", normal, committed},
		{"abort-result-identity-mismatch.json", "/internal/runtime/abort-quiesce", identityMismatch, active},
		{"abort-result-epoch-mismatch.json", "/internal/runtime/abort-quiesce", epochMismatch, active},
		{"abort-result-quiesce-mismatch.json", "/internal/runtime/abort-quiesce", quiesceMismatch, active},
		{"abort-result-token-mismatch.json", "/internal/runtime/abort-quiesce", tokenMismatch, active},
		{"abort-result-lease-expired.json", "/internal/runtime/abort-quiesce", normal, expired},
	}
	for _, tc := range cases {
		s, c := newManagementV1FixtureServer(t)
		tc.setup(s, c)
		fixtures[tc.file] = captureManagementFixture(t, s, http.MethodPost, tc.endpoint, tc.body)
	}
	return fixtures
}

func managementV1FixturePath(name string) string {
	return filepath.Join("..", "docs", "protocol", "samples", "management-file-read", name)
}

func TestManagementV1ObservedFixturesRoundTrip(t *testing.T) {
	for name, generated := range managementV1ObservedFixtures(t) {
		committed, err := os.ReadFile(managementV1FixturePath(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Equal(generated, committed) {
			t.Errorf("%s is not exact producer bytes; regenerate with CCCODEGEN_FIXTURES=1", name)
		}
	}
}

func TestManagementV1GenerateObservedFixtures(t *testing.T) {
	if os.Getenv("CCCODEGEN_FIXTURES") != "1" {
		t.Skip("set CCCODEGEN_FIXTURES=1 to write fixtures")
	}
	for name, generated := range managementV1ObservedFixtures(t) {
		if err := os.WriteFile(managementV1FixturePath(name), generated, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}
