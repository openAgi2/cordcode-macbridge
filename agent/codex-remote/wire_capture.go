package codexremote

// Flag-gated, redacted wire capture for disconnect-resilience plan evidence:
// E-12b (deployed wire shapes — plain/chunk/ack/pong seq fields) and E-9
// (ctrl token expiry distribution). Enabled by the marker file
// <dataDir>/codex-wire-capture.enable (env override
// CORDCODE_CODEX_WIRE_CAPTURE=<path>); writes JSONL to
// <dataDir>/logs/codex-remote-wire-capture.jsonl (or the env path).
//
// Redaction invariant (test-enforced): payload content, chunk fragments,
// tokens and pairing material are NEVER recorded — envelope headers, field
// presence (absent vs null preserved via the raw_shape view), sequence
// numbers, RPC method/id shape and redaction markers only. Capture failures
// disable the facility quietly: evidence collection must never break or slow
// the transport.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const wireCaptureMarkerFile = "codex-wire-capture.enable"

type wireCapture struct {
	mu   sync.Mutex
	path string
	f    *os.File
	dead bool
}

var (
	wireCaptureOnce sync.Once
	wireCap         *wireCapture
)

// initWireCaptureFromDataDir resolves the capture destination exactly once
// per process: env override wins, else the marker file inside the bridge data
// dir. Cheap to call from every wiring point (refresh, stream activation).
func initWireCaptureFromDataDir(dataDir string) bool {
	wireCaptureOnce.Do(func() {
		if p := os.Getenv("CORDCODE_CODEX_WIRE_CAPTURE"); p != "" {
			wireCap = newWireCapture(p)
			return
		}
		if dataDir == "" {
			return
		}
		if _, err := os.Stat(filepath.Join(dataDir, wireCaptureMarkerFile)); err != nil {
			return
		}
		wireCap = newWireCapture(filepath.Join(dataDir, "logs", "codex-remote-wire-capture.jsonl"))
	})
	return wireCap != nil
}

func resetWireCaptureForTest() {
	wireCaptureOnce = sync.Once{}
	wireCap = nil
}

func newWireCapture(path string) *wireCapture {
	wc := &wireCapture{path: path}
	// Meta line first: archives carry their own provenance header.
	wc.write(map[string]any{
		"kind": "capture_meta",
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
		"path": path,
		"note": "redacted wire capture: envelope headers/field shapes only; payload content, chunk fragments, tokens and pairing material are never recorded",
	})
	return wc
}

func (wc *wireCapture) write(entry map[string]any) {
	if wc == nil || wc.dead {
		return
	}
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if wc.f == nil {
		if err := os.MkdirAll(filepath.Dir(wc.path), 0o700); err != nil {
			wc.dead = true
			return
		}
		f, err := os.OpenFile(wc.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			wc.dead = true
			return
		}
		wc.f = f
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	if _, err := wc.f.Write(append(line, '\n')); err != nil {
		wc.dead = true
	}
}

// envelopeHeaderView records the struct-decoded view (strategy (a) of the
// E-12b dual-extraction discipline): which fields the production Envelope
// decodes, plus RPC method/id shape from the payload's top level.
func envelopeHeaderView(env Envelope) map[string]any {
	view := map[string]any{
		"type": env.Type,
	}
	if env.SeqID != nil {
		view["seq_id"] = *env.SeqID
	}
	if env.SegmentID != nil {
		view["segment_id"] = *env.SegmentID
	}
	if env.SegmentCount != nil {
		view["segment_count"] = *env.SegmentCount
	}
	if env.MessageSizeBytes != nil {
		view["message_size_bytes"] = *env.MessageSizeBytes
	}
	if env.Cursor != nil {
		view["cursor_present"] = true
	}
	if env.Status != "" {
		view["status"] = env.Status
	}
	if env.State != "" {
		view["state"] = env.State
	}
	view["stream_id"] = env.StreamID
	if n := len(env.Message); n > 0 {
		view["payload_bytes"] = n
	}
	if n := len(env.MessageChunkBase64); n > 0 {
		view["chunk_b64_len"] = n
	}
	// Payload top-level shape: key names plus the RPC method (a name, not
	// content) and the request id (a number) — enough to correlate
	// notification/response pairs without recording any result content.
	if len(env.Message) > 0 {
		var top map[string]json.RawMessage
		if json.Unmarshal(env.Message, &top) == nil {
			keys := make([]string, 0, len(top))
			for k := range top {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			view["payload_keys"] = keys
			if m, ok := top["method"]; ok {
				var method string
				if json.Unmarshal(m, &method) == nil && method != "" {
					view["payload_method"] = method
				}
			}
			if id, ok := top["id"]; ok {
				view["payload_id_raw"] = string(id)
			}
		}
	}
	return view
}

// captureInboundRaw records one inbound envelope with BOTH extraction views:
// the struct-decoded header view and raw_shape — the original wire JSON with
// the two content-bearing fields replaced by redaction markers. raw_shape
// preserves exact key presence (absent vs null) for the E-12b shape claims.
func captureInboundRaw(raw []byte, env Envelope) {
	if wireCap == nil {
		return
	}
	entry := map[string]any{
		"kind": "env_in",
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range envelopeHeaderView(env) {
		entry[k] = v
	}
	if len(raw) > 0 {
		var top map[string]json.RawMessage
		if json.Unmarshal(raw, &top) == nil {
			redacted := make(map[string]json.RawMessage, len(top))
			for k, v := range top {
				switch k {
				case "message":
					redacted[k] = json.RawMessage(`"<redacted:` + itoa(len(v)) + ` bytes>"`)
				case "message_chunk_base64":
					redacted[k] = json.RawMessage(`"<redacted:` + itoa(len(v)) + ` b64 chars>"`)
				default:
					redacted[k] = v
				}
			}
			if shape, err := json.Marshal(redacted); err == nil {
				entry["raw_shape"] = json.RawMessage(shape)
			}
		}
	}
	wireCap.write(entry)
}

// captureOutboundEnvelope records an envelope this controller actually wrote
// (ack frames — and, once S-3/S-7 land, chunk acks with SegmentID and probe
// acks). Outbound payloads (user prompts, RPC requests) are never captured:
// only the envelope header fields.
func captureOutboundEnvelope(env Envelope) {
	if wireCap == nil {
		return
	}
	entry := map[string]any{
		"kind": "env_out",
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range envelopeHeaderView(env) {
		entry[k] = v
	}
	wireCap.write(entry)
}

// captureCtrlExpiry records one E-9 sample: the ctrl token's observed expiry
// from an enroll/refresh/finish response, with the time-to-expiry measured at
// capture. The token value itself is never recorded.
func captureCtrlExpiry(ctrlExp, source string) {
	exp, ok := parseExpiresUnix(ctrlExp)
	if wireCap == nil {
		return
	}
	entry := map[string]any{
		"kind":   "ctrl_expiry",
		"ts":     time.Now().UTC().Format(time.RFC3339Nano),
		"source": source,
	}
	if ok {
		entry["ctrl_exp_unix"] = exp
		entry["expires_in_s"] = float64(exp - time.Now().Unix())
	} else {
		entry["ctrl_exp_parse_failed"] = ctrlExp == ""
	}
	wireCap.write(entry)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
