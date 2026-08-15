//go:build realdata

// P0 baseline fixtures capture: drives the REAL production Kernel hydrate +
// handleGetSessionProjection against owner-selected REAL on-disk transcripts
// (one per fixture class: tool-dense / oversized-tool-output / long-text) and
// dumps the raw full projection plus per-class baseline metrics. Not run by
// default `go test`; invoke with `-tags realdata` and
// CC_FIXTURE_CAPTURE_SPEC="class=path.jsonl,class=path.jsonl" plus
// CC_FIXTURE_CAPTURE_OUT=/tmp/dir. The transcript data and the hydrate/reduce/
// serve pipeline are real production; only the agent locator is stubbed to the
// file path (same pattern as handlers_projection_real_test.go).
package gobridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealProjectionFixtureCapture(t *testing.T) {
	spec := os.Getenv("CC_FIXTURE_CAPTURE_SPEC")
	outDir := os.Getenv("CC_FIXTURE_CAPTURE_OUT")
	if spec == "" || outDir == "" {
		t.Skip("CC_FIXTURE_CAPTURE_SPEC / CC_FIXTURE_CAPTURE_OUT not set")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, entry := range strings.Split(spec, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		if len(parts) != 2 {
			t.Fatalf("bad spec entry %q (want class=path)", entry)
		}
		class, rollout := parts[0], parts[1]
		sid := codexSessionIDFromRollout(rollout)

		handlers := NewHandlers()
		handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: rollout})

		// 1) cold full pull (the full/journal-miss baseline).
		conn := &readFileCaptureConn{}
		params, _ := json.Marshal(map[string]interface{}{"sessionId": sid, "sinceRev": 0})
		start := time.Now()
		handlers.handleGetSessionProjection(conn, WireMessage{
			RequestID: "fixture-" + class, BackendID: "codex",
			Method: "get_session_projection", Params: params,
		}, nil)
		fullElapsed := time.Since(start)
		if conn.err != nil {
			t.Fatalf("%s: cold pull failed: code=%s msg=%s", class, conn.err.Code, conn.err.Message)
		}
		dataMap, ok := conn.data.(map[string]interface{})
		if !ok {
			t.Fatalf("%s: served data not a map: %T", class, conn.data)
		}
		proj, ok := dataMap["projection"].(SessionProjection)
		if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
			t.Fatalf("%s: empty head-0 snapshot (forbidden)", class)
		}

		raw, err := json.MarshalIndent(proj, "", " ")
		if err != nil {
			t.Fatalf("%s: marshal projection: %v", class, err)
		}
		rawPath := filepath.Join(outDir, class+"-raw.json")
		if err := os.WriteFile(rawPath, raw, 0o644); err != nil {
			t.Fatalf("%s: write raw: %v", class, err)
		}

		waitForColdHydrateDrained(t, handlers, "codex", sid, 60*time.Second)

		// 2) at-head unchanged pull (the unchanged baseline).
		atHeadConn := &readFileCaptureConn{}
		atHeadParams, _ := json.Marshal(map[string]interface{}{"sessionId": sid, "sinceRev": proj.SyncRev})
		start = time.Now()
		handlers.handleGetSessionProjection(atHeadConn, WireMessage{
			RequestID: "fixture-" + class + "-athead", BackendID: "codex",
			Method: "get_session_projection", Params: atHeadParams,
		}, nil)
		atHeadElapsed := time.Since(start)
		if atHeadConn.err != nil {
			t.Fatalf("%s: at-head pull failed: %v", class, atHeadConn.err)
		}
		atHeadBytes := 0
		if b, err := json.Marshal(atHeadConn.data); err == nil {
			atHeadBytes = len(b)
		}

		metrics := map[string]interface{}{
			"class":         class,
			"sessionId":     sid,
			"sourceRollout": filepath.Base(rollout),
			"full": map[string]interface{}{
				"hydrateAndServeMs": fullElapsed.Milliseconds(),
				"projectionBytes":   len(raw),
				"turns":             len(proj.Turns),
				"syncRev":           proj.SyncRev,
			},
			"atHead": map[string]interface{}{
				"elapsedMs": fullElapsed.Milliseconds()*0 + atHeadElapsed.Milliseconds(),
				"wireBytes": atHeadBytes,
			},
		}
		metricsRaw, _ := json.MarshalIndent(metrics, "", " ")
		metricsPath := filepath.Join(outDir, class+"-metrics.json")
		if err := os.WriteFile(metricsPath, metricsRaw, 0o644); err != nil {
			t.Fatalf("%s: write metrics: %v", class, err)
		}
		fmt.Printf("REAL FIXTURE %s: sid=%s full=%d bytes in %s (turns=%d syncRev=%d); at-head %d bytes in %s\n",
			class, sid, len(raw), fullElapsed, len(proj.Turns), proj.SyncRev, atHeadBytes, atHeadElapsed)
	}
}
