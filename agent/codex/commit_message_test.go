package codex

import (
	"testing"
)

func TestUnwrapCodexStructuredOutput_DirectObject(t *testing.T) {
	raw := []byte(`{"message":"feat: add file"}`)
	got, err := unwrapCodexStructuredOutput(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(got) != `{"message":"feat: add file"}` {
		t.Fatalf("got %s", got)
	}
}

func TestUnwrapCodexStructuredOutput_DoubleEncodedString(t *testing.T) {
	// JSON string containing an object (legacy envelope).
	raw := []byte(`"{\"message\":\"feat: add file\"}"`)
	got, err := unwrapCodexStructuredOutput(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(got) != `{"message":"feat: add file"}` {
		t.Fatalf("got %s", got)
	}
}

func TestUnwrapCodexStructuredOutput_ObjectMustNotFailAsString(t *testing.T) {
	// Regression: old code json.Unmarshal into string →
	// "cannot unmarshal object into Go value of type string"
	raw := []byte(`{"message":"fix: blank body on first turn"}`)
	got, err := unwrapCodexStructuredOutput(raw)
	if err != nil {
		t.Fatalf("object envelope must be accepted: %v", err)
	}
	if len(got) == 0 || got[0] != '{' {
		t.Fatalf("want object payload, got %q", got)
	}
}
