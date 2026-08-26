package gobridge

// PERF-S4B regression: full vs window projection semantic parity on the canonical
// fixtures — including the codex-web (official-0.149.0-alpha.4 pipeline) and
// opencode-web (official-1.18.18 pipeline) web-perf fixtures. Walking window_0 →
// older* must reassemble EXACTLY the committed turn chain (same ids, same order, deep
// equality per turn) — windowing introduces zero semantic drift inside loaded coverage,
// and dedupe relies on stable turnId identity, never text equality.

import (
	"encoding/json"
	"os"
	"testing"
)

func readFileBytes(path string) ([]byte, error) { return os.ReadFile(path) }

func TestProjectionWindowParityFullVsWindowOnCanonicalFixtures(t *testing.T) {
	for name, path := range canonicalFixturePaths(t) {
		t.Run(name, func(t *testing.T) {
			data, err := readFileBytes(path)
			if err != nil {
				t.Fatal(err)
			}
			var projection SessionProjection
			if err := json.Unmarshal(data, &projection); err != nil {
				t.Fatal(err)
			}
			if len(projection.Turns) == 0 {
				t.Fatal("canonical fixture carries no turns")
			}

			// Window walk: window_0 (limit 3 forces multiple pages on every fixture) →
			// older until hasOlder=false. Pages arrive newest-page-first but carry their
			// turns oldest→newest; reverse each page so `walked` is strictly newest→oldest.
			var walked []TurnProjection
			appendReversed := func(turns []TurnProjection) {
				for index := len(turns) - 1; index >= 0; index-- {
					walked = append(walked, turns[index])
				}
			}
			response, err := sliceProjectionWindow("bench", projection.SessionID, windowTestEpoch, projection, GetSessionProjectionWindowParams{
				Direction: "window_0", Limit: 3,
			})
			if err != nil {
				t.Fatal(err)
			}
			appendReversed(response.Turns)
			cursor := response.Window.NextOlderCursor
			for cursor != "" {
				page, err := sliceProjectionWindow("bench", projection.SessionID, windowTestEpoch, projection, GetSessionProjectionWindowParams{
					Direction: "older", Cursor: cursor, Limit: 3,
				})
				if err != nil {
					t.Fatalf("older walk failed: %v", err)
				}
				appendReversed(page.Turns)
				cursor = page.Window.NextOlderCursor
			}

			if len(walked) != len(projection.Turns) {
				t.Fatalf("walk covered %d turns, full projection has %d", len(walked), len(projection.Turns))
			}
			for index, turn := range walked {
				full := projection.Turns[len(projection.Turns)-1-index]
				if turn.TurnID != full.TurnID {
					t.Fatalf("walked[%d] = %s, want chain-newest-first %s", index, turn.TurnID, full.TurnID)
				}
				wire, err := json.Marshal(turn)
				if err != nil {
					t.Fatal(err)
				}
				fullWire, err := json.Marshal(full)
				if err != nil {
					t.Fatal(err)
				}
				if string(wire) != string(fullWire) {
					t.Fatalf("turn %s drifted between full and window serving (identity parity, not text equality)", turn.TurnID)
				}
			}
		})
	}
}

// BenchmarkProjectionWindowOlderWalk — payload benchmark over the largest canonical
// fixture: window_0 + full older walk (limit 256) with per-page encode, proving the
// per-page cost stays bounded regardless of session size (S4B gate: payload budget).
func BenchmarkProjectionWindowOlderWalk(b *testing.B) {
	path := "../docs/protocol/samples/session-projection-v2/fixtures/long-text.json"
	data, err := readFileBytes(path)
	if err != nil {
		b.Fatal(err)
	}
	var projection SessionProjection
	if err := json.Unmarshal(data, &projection); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	var pages, bytes int
	for iteration := 0; iteration < b.N; iteration++ {
		pages = 0
		bytes = 0
		response, err := sliceProjectionWindow("bench", projection.SessionID, windowTestEpoch, projection, GetSessionProjectionWindowParams{
			Direction: "window_0", Limit: maxWindowTurns,
		})
		if err != nil {
			b.Fatal(err)
		}
		payload, _ := json.Marshal(response)
		pages++
		bytes = len(payload)
		cursor := response.Window.NextOlderCursor
		for cursor != "" {
			page, err := sliceProjectionWindow("bench", projection.SessionID, windowTestEpoch, projection, GetSessionProjectionWindowParams{
				Direction: "older", Cursor: cursor, Limit: maxWindowTurns,
			})
			if err != nil {
				b.Fatal(err)
			}
			payload, _ := json.Marshal(page)
			pages++
			bytes = len(payload)
			if len(payload) > maxWindowEncodedBytes {
				b.Fatalf("window page = %d bytes, want ≤ %d", len(payload), maxWindowEncodedBytes)
			}
			cursor = page.Window.NextOlderCursor
		}
	}
	b.ReportMetric(float64(pages), "pages/walk")
	b.ReportMetric(float64(bytes), "bytes/lastpage")
}
