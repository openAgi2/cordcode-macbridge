package codex

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Driver-level regression: generators must use core.UnmarshalJSONPayload, not
// string-only envelopes. Parsing is covered in core/structured_output_test.go;
// this file keeps a thin smoke that codex package still compiles against core.

func TestCodexCommitPayloadShapesViaCore(t *testing.T) {
	var parsed struct {
		Message string `json:"message"`
	}
	if err := core.UnmarshalJSONPayload([]byte(`{"message":"feat: object"}`), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Message != "feat: object" {
		t.Fatalf("message=%q", parsed.Message)
	}
	parsed.Message = ""
	if err := core.UnmarshalJSONPayload([]byte(`"{\"message\":\"feat: string\"}"`), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Message != "feat: string" {
		t.Fatalf("message=%q", parsed.Message)
	}
}
