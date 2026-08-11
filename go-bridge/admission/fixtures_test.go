package admission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// committed v1 fixtures 位于 macbridge docs/protocol/samples/management-file-read/（相对本包目录）。
func fixtureDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join("..", "..", "docs", "protocol", "samples", "management-file-read")
	if _, err := os.Stat(d); err != nil {
		t.Fatalf("fixture dir missing: %s (%v)", d, err)
	}
	return d
}

func readFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return json.RawMessage(b)
}

// 所有 committed request fixtures 必须被 Go strict codec 成功解码（success bytes）。
func TestFixtures_RequestsDecode(t *testing.T) {
	if _, err := DecodeQuiesceRequest(readFixture(t, "quiesce-request.json")); err != nil {
		t.Errorf("quiesce-request decode: %v", err)
	}
	if _, err := DecodeCommitRequest(readFixture(t, "commit-request.json")); err != nil {
		t.Errorf("commit-request decode: %v", err)
	}
	if _, err := DecodeCommitRequest(readFixture(t, "abort-request.json")); err != nil {
		t.Errorf("abort-request decode: %v", err)
	}
}

// 所有 committed result fixtures 必须按其 group strict-decode 成功，且 outcome 与文件名一致。
func TestFixtures_ResultsDecode(t *testing.T) {
	quiesce := map[string]string{ // file -> expected outcome
		"quiesce-result-safe.json": "safe", "quiesce-result-deferred.json": "deferred",
		"quiesce-result-identity-mismatch.json": "identity_mismatch",
		"quiesce-result-epoch-mismatch.json":    "epoch_mismatch",
		"quiesce-result-already-committed.json": "already_committed",
		"quiesce-result-already-quiescing.json": "already_quiescing",
		"quiesce-result-operation-reused.json":  "operation_reused",
		"quiesce-result-token-generation-failed.json": "token_generation_failed",
	}
	commit := map[string]string{
		"commit-result-committed.json": "committed",
		"commit-result-already-committed.json": "already_committed",
		"commit-result-identity-mismatch.json": "identity_mismatch",
		"commit-result-epoch-mismatch.json":    "epoch_mismatch",
		"commit-result-quiesce-mismatch.json":  "quiesce_mismatch",
		"commit-result-token-mismatch.json":    "token_mismatch",
		"commit-result-lease-expired.json":     "lease_expired",
	}
	abort := map[string]string{
		"abort-result-aborted.json": "aborted",
		"abort-result-already-accepting.json": "already_accepting",
		"abort-result-already-committed.json": "already_committed",
		"abort-result-identity-mismatch.json": "identity_mismatch",
		"abort-result-epoch-mismatch.json":    "epoch_mismatch",
		"abort-result-quiesce-mismatch.json":  "quiesce_mismatch",
		"abort-result-token-mismatch.json":    "token_mismatch",
		"abort-result-lease-expired.json":     "lease_expired",
	}
	for file, want := range quiesce {
		out, _, err := DecodeResultShape(readFixture(t, file), "quiesce")
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if out != want {
			t.Errorf("%s: outcome=%q want %q", file, out, want)
		}
	}
	for file, want := range commit {
		out, _, err := DecodeResultShape(readFixture(t, file), "commit")
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if out != want {
			t.Errorf("%s: outcome=%q want %q", file, out, want)
		}
	}
	for file, want := range abort {
		out, _, err := DecodeResultShape(readFixture(t, file), "abort")
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if out != want {
			t.Errorf("%s: outcome=%q want %q", file, out, want)
		}
	}
}

// negative mutations：对一份合法 commit-request 注入各类 strict 违规，必须被拒绝。
func TestFixtures_RequestNegativeCorpus(t *testing.T) {
	base := `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","expectedRuntime":{"pid":12345,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff"}`
	mutations := map[string]string{
		"duplicate key":      `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","operationId":"ffeeddccbbaa99887766554433221100","expectedRuntime":{"pid":12345,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff"}`,
		"unknown field":      `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","expectedRuntime":{"pid":12345,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff","extra":7}`,
		"missing field":      `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","expectedRuntime":{"pid":12345,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1}`,
		"null field":         `{"managementSchemaVersion":1,"operationId":null,"expectedRuntime":{"pid":12345,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff"}`,
		"quoted number":      `{"managementSchemaVersion":"1","operationId":"ffeeddccbbaa99887766554433221100","expectedRuntime":{"pid":12345,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff"}`,
		"negative number":    `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","expectedRuntime":{"pid":-1,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff"}`,
		"float pid":          `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","expectedRuntime":{"pid":12.5,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff"}`,
		"uppercase hex op":   `{"managementSchemaVersion":1,"operationId":"FFEEDDCCBBAA99887766554433221100","expectedRuntime":{"pid":12345,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff"}`,
		"bad token hex":      `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","expectedRuntime":{"pid":12345,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"ZZ112233445566778899aabbccddeeff"}`,
		"short op":           `{"managementSchemaVersion":1,"operationId":"ff","expectedRuntime":{"pid":12345,"bridgeEpoch":1},"expectedHealthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff"}`,
	}
	// base must decode OK first
	if _, err := DecodeCommitRequest(json.RawMessage(base)); err != nil {
		t.Fatalf("base fixture must decode OK: %v", err)
	}
	for name, body := range mutations {
		if _, err := DecodeCommitRequest(json.RawMessage(body)); err == nil {
			t.Errorf("mutation %q accepted but must be rejected", name)
		}
	}
}

// result negative：往 safe 结果塞 extra 字段或改 outcome 类型，必须被拒绝。
func TestFixtures_ResultNegativeCorpus(t *testing.T) {
	safe := `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","outcome":"safe","runtimeIdentity":{"pid":12345,"bridgeEpoch":1},"healthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff","leaseMillis":30000,"leaseRemainingMillis":29000}`
	if _, _, err := DecodeResultShape(json.RawMessage(safe), "quiesce"); err != nil {
		t.Fatalf("base safe result must decode OK: %v", err)
	}
	extra := `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","outcome":"safe","runtimeIdentity":{"pid":12345,"bridgeEpoch":1},"healthEpoch":1,"quiesceEpoch":1,"token":"00112233445566778899aabbccddeeff","leaseMillis":30000,"leaseRemainingMillis":29000,"leaked":99}`
	if _, _, err := DecodeResultShape(json.RawMessage(extra), "quiesce"); err == nil {
		t.Error("safe result with extra field must be rejected")
	}
	// identity_mismatch 不允许带 safe 的额外字段
	badCombo := `{"managementSchemaVersion":1,"operationId":"ffeeddccbbaa99887766554433221100","outcome":"identity_mismatch","token":"00112233445566778899aabbccddeeff"}`
	if _, _, err := DecodeResultShape(json.RawMessage(badCombo), "quiesce"); err == nil {
		t.Error("identity_mismatch result with token field must be rejected (cross-kind extra)")
	}
}
