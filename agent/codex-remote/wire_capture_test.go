package codexremote

// wire_capture_test.go enforces the capture facility's redaction invariant
// (disconnect-resilience plan E-12b discipline): payload content, chunk
// fragments and tokens must never reach the capture file, while the field
// shapes — including absent-vs-null presence in raw_shape — must.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func enableWireCaptureForTest(t *testing.T) string {
	t.Helper()
	resetWireCaptureForTest()
	path := filepath.Join(t.TempDir(), "capture.jsonl")
	t.Setenv("CORDCODE_CODEX_WIRE_CAPTURE", path)
	initWireCaptureFromDataDir("")
	if wireCap == nil {
		t.Fatal("wire capture not enabled via env override")
	}
	t.Cleanup(resetWireCaptureForTest)
	return path
}

func captureLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("capture line not JSON: %v (%s)", err, line)
		}
		lines = append(lines, m)
	}
	return lines
}

func TestWireCaptureInboundRedactsContentKeepsShapes(t *testing.T) {
	path := enableWireCaptureForTest(t)

	raw := []byte(`{"type":"server_message","client_id":"cl-1","env_id":"env-1","stream_id":"st-1",` +
		`"seq_id":42,"message":{"id":7,"method":"thread/loaded/list","params":{"secret":"do not record me"}}}`)
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	captureInboundRaw(raw, env)

	chunkRaw := []byte(`{"type":"server_message_chunk","seq_id":43,"segment_id":1,"segment_count":3,` +
		`"message_size_bytes":250000,"message_chunk_base64":"U0VDUkVUX0ZSQUdNRU5U"}`)
	var chunk Envelope
	if err := json.Unmarshal(chunkRaw, &chunk); err != nil {
		t.Fatal(err)
	}
	captureInboundRaw(chunkRaw, chunk)

	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "do not record me") || strings.Contains(string(blob), "SECRET_FRAGMENT") {
		t.Fatalf("payload content leaked into capture:\n%s", blob)
	}

	lines := captureLines(t, path)
	if len(lines) != 3 { // meta + env_in + env_in
		t.Fatalf("capture lines = %d, want 3 (meta + 2 envelopes)", len(lines))
	}
	plain := lines[1]
	if plain["type"] != "server_message" || plain["seq_id"].(float64) != 42 {
		t.Fatalf("plain header view wrong: %v", plain)
	}
	// Struct view: RPC method recorded (a name, not content), no result body.
	if plain["payload_method"] != "thread/loaded/list" {
		t.Fatalf("payload_method missing: %v", plain)
	}
	// Raw shape: exact key set with message redacted in place.
	shape, ok := plain["raw_shape"].(map[string]any)
	if !ok {
		t.Fatalf("raw_shape missing: %v", plain)
	}
	if _, has := shape["segment_id"]; has {
		t.Fatalf("plain frame must not carry segment keys in raw_shape: %v", shape)
	}
	redacted, _ := shape["message"].(string)
	if !strings.HasPrefix(redacted, "<redacted:") {
		t.Fatalf("message not redacted in raw_shape: %q", redacted)
	}

	chunkLine := lines[2]
	if chunkLine["segment_id"].(float64) != 1 || chunkLine["segment_count"].(float64) != 3 {
		t.Fatalf("chunk header view wrong: %v", chunkLine)
	}
	chunkShape := chunkLine["raw_shape"].(map[string]any)
	if chunkShape["segment_id"].(float64) != 1 {
		t.Fatalf("chunk raw_shape lost segment_id: %v", chunkShape)
	}
	if b64, _ := chunkShape["message_chunk_base64"].(string); !strings.HasPrefix(b64, "<redacted:") {
		t.Fatalf("chunk fragment not redacted: %q", b64)
	}
}

func TestWireCaptureCtrlExpiryRecordsDistributionNotToken(t *testing.T) {
	path := enableWireCaptureForTest(t)

	future := time.Now().Add(30 * time.Minute).Unix()
	captureCtrlExpiry(itoa(int(future)), "refresh/finish")
	captureCtrlExpiry("", "refresh/finish")

	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "remote_control_token") {
		t.Fatalf("token field name leaked into capture:\n%s", blob)
	}
	lines := captureLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("capture lines = %d, want 3", len(lines))
	}
	sample := lines[1]
	if sample["kind"] != "ctrl_expiry" || sample["source"] != "refresh/finish" {
		t.Fatalf("ctrl expiry entry wrong: %v", sample)
	}
	if expiresIn, _ := sample["expires_in_s"].(float64); expiresIn < 1700 || expiresIn > 1800 {
		t.Fatalf("expires_in_s = %v, want ~1800", sample["expires_in_s"])
	}
	// Unparseable expiry is recorded as an honest failure marker, not dropped.
	if parseFailed, _ := lines[2]["ctrl_exp_parse_failed"].(bool); !parseFailed {
		t.Fatalf("unparseable expiry not marked: %v", lines[2])
	}
}

func TestWireCaptureDisabledWithoutMarkerOrEnv(t *testing.T) {
	resetWireCaptureForTest()
	t.Setenv("CORDCODE_CODEX_WIRE_CAPTURE", "")
	if initWireCaptureFromDataDir(t.TempDir()) {
		t.Fatal("capture must stay disabled without marker file")
	}
	// Even with a dataDir marker absent, calls must be silent no-ops.
	captureInboundRaw([]byte(`{"type":"pong","seq_id":1}`), Envelope{})
	captureCtrlExpiry("123", "test")
}
