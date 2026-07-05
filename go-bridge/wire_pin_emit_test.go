package gobridge

import (
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSessionsToWireEmitsPinnedAtMillis(t *testing.T) {
	pt := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	sessions := []core.AgentSessionInfo{
		{ID: "pinned-one", Summary: "s1", PinnedAt: pt},
		{ID: "unpinned", Summary: "s2"}, // no PinnedAt -> field omitted
	}
	wire := sessionsToWire(sessions)
	if len(wire) != 2 {
		t.Fatalf("want 2 wire maps, got %d", len(wire))
	}
	got, ok := wire[0]["pinnedAtMillis"]
	if !ok {
		t.Fatal("pinned session missing pinnedAtMillis")
	}
	if got != pt.UnixMilli() {
		t.Fatalf("pinnedAtMillis=%v want=%d", got, pt.UnixMilli())
	}
	if _, present := wire[1]["pinnedAtMillis"]; present {
		t.Fatal("unpinned session must omit pinnedAtMillis")
	}
}

func TestMapSessionEmitsPinnedAtMillisWhenSourceHasIt(t *testing.T) {
	src := map[string]interface{}{
		"id":             "s1",
		"title":          "t",
		"pinnedAtMillis": float64(1783200000000),
	}
	out := mapSession(src)
	got, ok := out["pinnedAtMillis"]
	if !ok {
		t.Fatal("mapSession dropped source pinnedAtMillis")
	}
	if got != int64(1783200000000) {
		t.Fatalf("pinnedAtMillis=%v want int64(1783200000000)", got)
	}

	// Source without pin -> field omitted.
	out2 := mapSession(map[string]interface{}{"id": "s2", "title": "t"})
	if _, present := out2["pinnedAtMillis"]; present {
		t.Fatal("mapSession emitted pinnedAtMillis for non-pinned source")
	}
}
