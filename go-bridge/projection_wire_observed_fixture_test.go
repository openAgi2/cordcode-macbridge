package gobridge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

var canonicalProjectionPatchFields = []string{
	"baseRev", "syncRev", "execution", "upsertTurns", "partOps", "replacesClientIds",
}

func observedProjectionFixture(t *testing.T, name string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "docs", "protocol", "samples", "session-projection-v2", name))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func rawObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func rawArray(t *testing.T, raw json.RawMessage) []json.RawMessage {
	t.Helper()
	var array []json.RawMessage
	if err := json.Unmarshal(raw, &array); err != nil {
		t.Fatal(err)
	}
	return array
}

func sortedRawKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func semanticJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// Independent check 1: inspect the committed wire objects without using ProjectionPatch.
// Presence is evidence: absent optional fields must not be normalized to empty arrays.
func TestObservedProjectionWireKeyPresenceAndPairing(t *testing.T) {
	push := observedProjectionFixture(t, "projection-patch-observed.json")
	pull := observedProjectionFixture(t, "get-session-projection-delta-observed.json")
	if got, want := sortedRawKeys(push), []string{"backendId", "bridgeEpoch", "data", "event", "eventId", "perSessionSeq", "replayable", "seq", "sessionId", "timestamp", "type"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("push envelope keys = %v, want %v", got, want)
	}
	if got, want := sortedRawKeys(pull), []string{"data", "ok", "requestId", "type"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pull envelope keys = %v, want %v", got, want)
	}
	pullData := rawObject(t, pull["data"])
	if got, want := sortedRawKeys(pullData), []string{"headRev", "patches"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pull data keys = %v, want %v", got, want)
	}
	patches := rawArray(t, pullData["patches"])
	if len(patches) != 3 {
		t.Fatalf("patch count = %d, want 3", len(patches))
	}
	pushPatch := rawObject(t, push["data"])
	for index, patchRaw := range patches {
		patch := rawObject(t, patchRaw)
		for _, key := range canonicalProjectionPatchFields {
			_, present := patch[key]
			wantPresent := key != "partOps" && key != "replacesClientIds"
			if present != wantPresent {
				t.Errorf("patch[%d] field %s presence=%t, want %t", index, key, present, wantPresent)
			}
		}
	}
	if !reflect.DeepEqual(semanticJSON(t, push["data"]), semanticJSON(t, patches[0])) {
		t.Fatal("captured push data is not identical to the first pulled journal patch")
	}
	if !reflect.DeepEqual(sortedRawKeys(pushPatch), sortedRawKeys(rawObject(t, patches[0]))) {
		t.Fatal("paired push/pull patch key presence differs")
	}
}

// Independent check 2: decode every observed patch through the canonical type, re-encode it,
// and compare each of the six fields semantically and for presence.
func TestObservedProjectionWireCanonicalTypedRoundTrip(t *testing.T) {
	push := observedProjectionFixture(t, "projection-patch-observed.json")
	pull := observedProjectionFixture(t, "get-session-projection-delta-observed.json")
	pullData := rawObject(t, pull["data"])
	patches := append([]json.RawMessage{push["data"]}, rawArray(t, pullData["patches"])...)
	for index, raw := range patches {
		var typed ProjectionPatch
		if err := json.Unmarshal(raw, &typed); err != nil {
			t.Fatalf("patch[%d] typed decode: %v", index, err)
		}
		encoded, err := json.Marshal(typed)
		if err != nil {
			t.Fatalf("patch[%d] typed encode: %v", index, err)
		}
		before := rawObject(t, raw)
		after := rawObject(t, encoded)
		for _, key := range canonicalProjectionPatchFields {
			beforeRaw, beforePresent := before[key]
			afterRaw, afterPresent := after[key]
			if beforePresent != afterPresent {
				t.Errorf("patch[%d] field %s presence changed %t→%t", index, key, beforePresent, afterPresent)
				continue
			}
			if beforePresent && !reflect.DeepEqual(semanticJSON(t, beforeRaw), semanticJSON(t, afterRaw)) {
				t.Errorf("patch[%d] field %s changed after typed round-trip", index, key)
			}
		}
	}
}
