package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnwrapJSONPayload_DirectObject(t *testing.T) {
	raw := []byte(`{"message":"feat: add file"}`)
	got, err := UnwrapJSONPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"message":"feat: add file"}` {
		t.Fatalf("got %s", got)
	}
}

func TestUnwrapJSONPayload_DirectArray(t *testing.T) {
	raw := []byte(`[1,2]`)
	got, err := UnwrapJSONPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[1,2]` {
		t.Fatalf("got %s", got)
	}
}

func TestUnwrapJSONPayload_DoubleEncodedString(t *testing.T) {
	raw := []byte(`"{\"message\":\"feat: add file\"}"`)
	got, err := UnwrapJSONPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"message":"feat: add file"}` {
		t.Fatalf("got %s", got)
	}
}

func TestUnwrapJSONPayload_ObjectMustNotFailAsString(t *testing.T) {
	// Regression: naive json.Unmarshal into string → cannot unmarshal object into Go value of type string
	raw := []byte(`{"message":"fix: blank body on first turn"}`)
	var wrong string
	if err := json.Unmarshal(raw, &wrong); err == nil {
		t.Fatal("sanity: unmarshaling object into string should fail")
	}
	if _, err := UnwrapJSONPayload(raw); err != nil {
		t.Fatalf("UnwrapJSONPayload must accept object: %v", err)
	}
}

func TestUnwrapJSONPayload_Empty(t *testing.T) {
	if _, err := UnwrapJSONPayload(nil); err == nil {
		t.Fatal("want error")
	}
	if _, err := UnwrapJSONPayload([]byte("null")); err == nil {
		t.Fatal("want error for null")
	}
}

func TestUnmarshalJSONPayload_CommitMessage(t *testing.T) {
	var parsed struct {
		Message string `json:"message"`
	}
	if err := UnmarshalJSONPayload([]byte(`{"message":"chore: x"}`), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Message != "chore: x" {
		t.Fatalf("message=%q", parsed.Message)
	}
	parsed = struct {
		Message string `json:"message"`
	}{}
	if err := UnmarshalJSONPayload([]byte(`"{\"message\":\"chore: y\"}"`), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Message != "chore: y" {
		t.Fatalf("message=%q", parsed.Message)
	}
}

func TestExtractClaudePrintStructuredPayload_ResultObject(t *testing.T) {
	stdout := []byte(`{"type":"result","result":{"message":"feat: from result"}}`)
	got, err := ExtractClaudePrintStructuredPayload(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "feat: from result") {
		t.Fatalf("got %s", got)
	}
}

func TestExtractClaudePrintStructuredPayload_StructuredOutputString(t *testing.T) {
	stdout := []byte(`{"structured_output":"{\"message\":\"feat: from so\"}"}`)
	got, err := ExtractClaudePrintStructuredPayload(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "feat: from so") {
		t.Fatalf("got %s", got)
	}
}

func TestExtractClaudePrintStructuredPayload_ResultStringPreferOverEmptySO(t *testing.T) {
	stdout := []byte(`{"result":"{\"title\":\"T\",\"body\":\"B\"}","structured_output":null}`)
	var parsed struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := UnmarshalClaudePrintStructured(stdout, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Title != "T" || parsed.Body != "B" {
		t.Fatalf("parsed=%+v", parsed)
	}
}

func TestExtractClaudePrintStructuredPayload_MissingFields(t *testing.T) {
	if _, err := ExtractClaudePrintStructuredPayload([]byte(`{"type":"result"}`)); err == nil {
		t.Fatal("want error")
	}
}
