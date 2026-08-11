package core

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnwrapJSONPayload normalizes agent / CLI structured-output bytes into a JSON
// value payload suitable for a second json.Unmarshal into a typed struct.
//
// Accepted shapes (all common across Claude print JSON and Codex
// --output-last-message / tool arguments):
//
//  1. Direct JSON object or array:  {"message":"..."}  or  [...]
//  2. JSON string containing JSON:  "{\"message\":\"...\"}"
//
// Rejects empty input and unexpected prefixes. Drivers must NOT assume only one
// shape — CLI versions flip between (1) and (2).
func UnwrapJSONPayload(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("empty structured output")
	}
	switch trimmed[0] {
	case '{', '[':
		return trimmed, nil
	case '"':
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return nil, fmt.Errorf("unwrap JSON string envelope: %w", err)
		}
		inner := bytes.TrimSpace([]byte(encoded))
		if len(inner) == 0 {
			return nil, fmt.Errorf("empty structured output inside string envelope")
		}
		return inner, nil
	default:
		prefix := trimmed
		if len(prefix) > 24 {
			prefix = prefix[:24]
		}
		return nil, fmt.Errorf("unexpected structured output prefix %q", string(prefix))
	}
}

// UnmarshalJSONPayload unwraps then unmarshals into out.
func UnmarshalJSONPayload(raw []byte, out any) error {
	payload, err := UnwrapJSONPayload(raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode structured payload: %w", err)
	}
	return nil
}

// ExtractClaudePrintStructuredPayload pulls the structured object from a Claude
// CLI `-p --output-format json` envelope.
//
// The CLI may put the schema object under "result" and/or "structured_output",
// and each field may itself be either a JSON object or a JSON-encoded string.
func ExtractClaudePrintStructuredPayload(stdout []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty claude print output")
	}
	var envelope struct {
		Result           json.RawMessage `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, fmt.Errorf("claude print envelope: %w", err)
	}
	raw := bytes.TrimSpace(envelope.Result)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		raw = bytes.TrimSpace(envelope.StructuredOutput)
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, fmt.Errorf("claude print envelope missing result/structured_output")
	}
	return UnwrapJSONPayload(raw)
}

// UnmarshalClaudePrintStructured unwraps a Claude print JSON envelope into out.
func UnmarshalClaudePrintStructured(stdout []byte, out any) error {
	payload, err := ExtractClaudePrintStructuredPayload(stdout)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode claude structured payload: %w", err)
	}
	return nil
}
