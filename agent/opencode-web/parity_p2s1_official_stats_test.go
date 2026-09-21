package opencodeweb

import (
	"encoding/json"
	"testing"
)

// Parity plan v2.12 slice P2+S1 (§5.2 MacBridge 验收): opencode-web official
// stats / read display mapping — filediff 0-value preservation, apply_patch
// multi-file entries, display file mapping, directory fail-closed, mixed-pair
// independence, optionalInt narrowing, and step-level field attachment.

// TestFileDiffZeroCountsPreserved: a legitimate 0 (additions=0 has 125 real
// samples, deletions=0 has 355) must be written verbatim as int 0 — pointer
// semantics, never dropped by omitempty-style logic.
func TestFileDiffZeroCountsPreserved(t *testing.T) {
	state := map[string]any{
		"metadata": map[string]any{
			"filediff": map[string]any{
				"file":       "src/main.go",
				"patch":      "+a\n-b\n",
				"additions":  float64(0),
				"deletions":  float64(0),
			},
		},
	}
	changes := fileChangesFromToolState(state)
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(changes))
	}
	additions, ok := changes[0]["additions"].(int)
	if !ok || additions != 0 {
		t.Fatalf("additions = %#v, want int 0", changes[0]["additions"])
	}
	deletions, ok := changes[0]["deletions"].(int)
	if !ok || deletions != 0 {
		t.Fatalf("deletions = %#v, want int 0", changes[0]["deletions"])
	}
}

// TestFileDiffMixedPairCarriedAsIs: independent optionals — additions without
// deletions is carried as-is; the complete-pair rule is the iOS consumer's job.
func TestFileDiffMixedPairCarriedAsIs(t *testing.T) {
	state := map[string]any{
		"metadata": map[string]any{
			"filediff": map[string]any{
				"file":      "a.txt",
				"patch":     "+x",
				"additions": float64(2),
			},
		},
	}
	changes := fileChangesFromToolState(state)
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(changes))
	}
	if got, ok := changes[0]["additions"].(int); !ok || got != 2 {
		t.Fatalf("additions = %#v, want int 2", changes[0]["additions"])
	}
	if _, present := changes[0]["deletions"]; present {
		t.Fatalf("deletions unexpectedly present: %#v", changes[0]["deletions"])
	}
	// (nil, 3) mirror.
	state2 := map[string]any{
		"metadata": map[string]any{
			"filediff": map[string]any{
				"file":      "b.txt",
				"patch":     "-y",
				"deletions": float64(3),
			},
		},
	}
	changes2 := fileChangesFromToolState(state2)
	if got, ok := changes2[0]["deletions"].(int); !ok || got != 3 {
		t.Fatalf("deletions = %#v, want int 3", changes2[0]["deletions"])
	}
	if _, present := changes2[0]["additions"]; present {
		t.Fatalf("additions unexpectedly present: %#v", changes2[0]["additions"])
	}
}

// TestFileDiffNonIntegralNumberNotCounted: 2.5 is not an integer count — must
// not be mapped (fail closed, no truncation).
func TestFileDiffNonIntegralNumberNotCounted(t *testing.T) {
	state := map[string]any{
		"metadata": map[string]any{
			"filediff": map[string]any{
				"file":      "c.txt",
				"patch":     "+z",
				"additions": float64(2.5),
			},
		},
	}
	changes := fileChangesFromToolState(state)
	if _, present := changes[0]["additions"]; present {
		t.Fatalf("non-integral additions mapped: %#v", changes[0]["additions"])
	}
}

// TestApplyPatchMetadataFilesMultiEntry: metadata.files (apply_patch) maps to
// one fileChange per entry, kind = official type (add/update/delete), movePath
// carried, per-file official counts carried.
func TestApplyPatchMetadataFilesMultiEntry(t *testing.T) {
	state := map[string]any{
		"metadata": map[string]any{
			"files": []any{
				map[string]any{
					"filePath":   "new.go",
					"type":       "add",
					"patch":      "+package main\n",
					"additions":  float64(10),
					"deletions":  float64(0),
				},
				map[string]any{
					"filePath":   "moved.go",
					"type":       "update",
					"patch":      "+q\n-r\n",
					"movePath":   "renamed.go",
					"additions":  float64(1),
					"deletions":  float64(1),
				},
				map[string]any{
					"relativePath": "old.go",
					"type":         "delete",
					"deletions":    float64(42),
				},
			},
		},
	}
	changes := fileChangesFromToolState(state)
	if len(changes) != 3 {
		t.Fatalf("changes = %d, want 3 (got %+v)", len(changes), changes)
	}
	byPath := map[string]map[string]any{}
	for _, change := range changes {
		byPath[change["path"].(string)] = change
	}
	if kind := byPath["new.go"]["kind"]; kind != "add" {
		t.Fatalf("new.go kind = %#v, want add", kind)
	}
	if got, ok := byPath["new.go"]["additions"].(int); !ok || got != 10 {
		t.Fatalf("new.go additions = %#v, want int 10", byPath["new.go"]["additions"])
	}
	if got, ok := byPath["new.go"]["deletions"].(int); !ok || got != 0 {
		t.Fatalf("new.go deletions = %#v, want int 0 (legitimate zero)", byPath["new.go"]["deletions"])
	}
	if kind := byPath["moved.go"]["kind"]; kind != "update" {
		t.Fatalf("moved.go kind = %#v, want update", kind)
	}
	if move := byPath["moved.go"]["movePath"]; move != "renamed.go" {
		t.Fatalf("moved.go movePath = %#v, want renamed.go", move)
	}
	if kind := byPath["old.go"]["kind"]; kind != "delete" {
		t.Fatalf("old.go kind = %#v, want delete", kind)
	}
	if _, present := byPath["old.go"]["additions"]; present {
		t.Fatalf("old.go additions unexpectedly present")
	}
}

// TestApplyPatchFilesPreferredOverFilediff: metadata.files wins when both
// sources exist (apply_patch shape).
func TestApplyPatchFilesPreferredOverFilediff(t *testing.T) {
	state := map[string]any{
		"metadata": map[string]any{
			"files": []any{
				map[string]any{"filePath": "f1.go", "type": "add"},
			},
			"filediff": map[string]any{
				"file":  "other.go",
				"patch": "+o",
			},
		},
	}
	changes := fileChangesFromToolState(state)
	if len(changes) != 1 || changes[0]["path"] != "f1.go" {
		t.Fatalf("changes = %+v, want single f1.go", changes)
	}
}

// TestFileDisplayFileTypeMapped: display.type == "file" maps all official
// fields verbatim (no Mac-side validation/clamping — iOS consumer validates).
func TestFileDisplayFileTypeMapped(t *testing.T) {
	state := map[string]any{
		"metadata": map[string]any{
			"display": map[string]any{
				"type":       "file",
				"path":       "src/read.go",
				"text":       "package main\n",
				"lineStart":  float64(1),
				"lineEnd":    float64(1),
				"totalLines": float64(120),
				"truncated":  true,
			},
		},
	}
	display := fileDisplayFromToolState(state)
	if display == nil {
		t.Fatal("display not mapped")
	}
	if display["path"] != "src/read.go" || display["text"] != "package main\n" {
		t.Fatalf("display = %#v", display)
	}
	if got, ok := display["lineStart"].(int); !ok || got != 1 {
		t.Fatalf("lineStart = %#v, want int 1", display["lineStart"])
	}
	if got, ok := display["lineEnd"].(int); !ok || got != 1 {
		t.Fatalf("lineEnd = %#v, want int 1", display["lineEnd"])
	}
	if got, ok := display["totalLines"].(int); !ok || got != 120 {
		t.Fatalf("totalLines = %#v, want int 120", display["totalLines"])
	}
	if truncated, ok := display["truncated"].(bool); !ok || !truncated {
		t.Fatalf("truncated = %#v, want true", display["truncated"])
	}
}

// TestFileDisplayDirectoryNotMapped: directory display is a distinct upstream
// shape with no real sample — non-file display types must NOT map (fail
// closed; iOS keeps the output-tier rendering).
func TestFileDisplayDirectoryNotMapped(t *testing.T) {
	for _, displayType := range []string{"directory", "diff", ""} {
		state := map[string]any{
			"metadata": map[string]any{
				"display": map[string]any{
					"type":  displayType,
					"path":  "src/",
					"files": []any{},
				},
			},
		}
		if got := fileDisplayFromToolState(state); got != nil {
			t.Fatalf("display type %q mapped: %#v", displayType, got)
		}
	}
}

// TestFileDisplayAbsentMetadata: no metadata / no display → nil (read tools
// from servers without display keep the current shape).
func TestFileDisplayAbsentMetadata(t *testing.T) {
	if got := fileDisplayFromToolState(map[string]any{}); got != nil {
		t.Fatalf("empty state mapped: %#v", got)
	}
	if got := fileDisplayFromToolState(map[string]any{"metadata": map[string]any{}}); got != nil {
		t.Fatalf("metadata without display mapped: %#v", got)
	}
}

// TestOptionalIntNarrowing: only integral JSON numbers count.
func TestOptionalIntNarrowing(t *testing.T) {
	cases := []struct {
		value any
		want  int
		ok    bool
	}{
		{float64(3), 3, true},
		{float64(0), 0, true},
		{float64(-1), -1, true},
		{float64(2.5), 0, false},
		{float32(7), 7, true},
		{float32(2.5), 0, false},
		{int(9), 9, true},
		{int64(11), 11, true},
		{json.Number("13"), 13, true},
		{json.Number("2.5"), 0, false},
		{"15", 0, false},
		{true, 0, false},
		{nil, 0, false},
	}
	for _, tc := range cases {
		got, ok := optionalInt(tc.value)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("optionalInt(%#v) = (%d, %v), want (%d, %v)", tc.value, got, ok, tc.want, tc.ok)
		}
	}
}

// TestMapToolStepFromPartCarriesStructuredFields: the composed step attaches
// fileChanges and fileDisplay when the official metadata provides them.
func TestMapToolStepFromPartCarriesStructuredFields(t *testing.T) {
	part := map[string]any{
		"tool": "read",
		"state": map[string]any{
			"status": "completed",
			"output": "file content",
			"metadata": map[string]any{
				"display": map[string]any{
					"type":       "file",
					"path":       "a.txt",
					"text":       "hello\n",
					"lineStart":  float64(1),
					"lineEnd":    float64(1),
					"totalLines": float64(1),
				},
				"filediff": map[string]any{
					"file":      "a.txt",
					"patch":     "+hello",
					"additions": float64(1),
				},
			},
		},
	}
	step := mapToolStepFromPart(part)
	if step == nil {
		t.Fatal("step nil")
	}
	changes, ok := step["fileChanges"].([]map[string]any)
	if !ok || len(changes) != 1 {
		t.Fatalf("step.fileChanges = %#v", step["fileChanges"])
	}
	if got, ok := changes[0]["additions"].(int); !ok || got != 1 {
		t.Fatalf("fileChanges[0].additions = %#v, want int 1", changes[0]["additions"])
	}
	display, ok := step["fileDisplay"].(map[string]any)
	if !ok || display["path"] != "a.txt" {
		t.Fatalf("step.fileDisplay = %#v", step["fileDisplay"])
	}
}
