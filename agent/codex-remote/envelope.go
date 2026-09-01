package codexremote

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Frozen controller protocol v3 constants from
// testdata/phase0/static-26.825.32147-alpha.12.2/controller-call-sites.json
// and live attempt-008.
const (
	ProtocolVersion            = "3"
	SegmentTargetBytes         = 102400
	WireEnvelopeMaxBytes       = 153600
	ReassembledMessageMaxBytes = 1073741824
	MaxConcurrentAssemblies    = 128
	MaxSegments                = 1024
)

const (
	typeClientMessage      = "client_message"
	typeClientMessageChunk = "client_message_chunk"
	typeAck                = "ack"
	typePing               = "ping"
	typeClientClosed       = "client_closed"
	typeServerMessage      = "server_message"
	typeServerMessageChunk = "server_message_chunk"
	typePong               = "pong"
)

// Envelope is the controller WSS frame. Field names match the live probe
// (snake_case client_id/env_id/stream_id/seq_id).
type Envelope struct {
	Type               string          `json:"type"`
	ClientID           string          `json:"client_id,omitempty"`
	EnvID              string          `json:"env_id,omitempty"`
	StreamID           string          `json:"stream_id,omitempty"`
	SeqID              *uint64         `json:"seq_id,omitempty"`
	Cursor             *string         `json:"cursor,omitempty"`
	SkipHistory        bool            `json:"skip_history,omitempty"`
	Message            json.RawMessage `json:"message,omitempty"`
	Status             string          `json:"status,omitempty"`
	State              string          `json:"state,omitempty"`
	SegmentID          *int            `json:"segment_id,omitempty"`
	SegmentCount       *int            `json:"segment_count,omitempty"`
	MessageSizeBytes   *int            `json:"message_size_bytes,omitempty"`
	MessageChunkBase64 string          `json:"message_chunk_base64,omitempty"`
}

func (e Envelope) routingOK(clientID, envID, streamID string) error {
	if e.ClientID != "" && e.ClientID != clientID {
		return fmt.Errorf("codex-remote: client_id mismatch")
	}
	if e.EnvID != "" && e.EnvID != envID {
		return fmt.Errorf("codex-remote: env_id mismatch")
	}
	if e.StreamID != "" && e.StreamID != streamID {
		return fmt.Errorf("codex-remote: stream_id mismatch")
	}
	return nil
}

func splitPayload(payload []byte) [][]byte {
	if len(payload) == 0 {
		return [][]byte{payload}
	}
	if len(payload) <= SegmentTargetBytes {
		return [][]byte{payload}
	}
	var parts [][]byte
	for i := 0; i < len(payload); i += SegmentTargetBytes {
		end := i + SegmentTargetBytes
		if end > len(payload) {
			end = len(payload)
		}
		parts = append(parts, payload[i:end])
	}
	return parts
}

func encodeChunk(part []byte) string {
	return base64.StdEncoding.EncodeToString(part)
}

func decodeChunk(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
