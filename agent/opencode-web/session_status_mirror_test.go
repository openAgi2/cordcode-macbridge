package opencodeweb

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// session_status_mirror_test.go —— opencode-web v4 方案 Track A 回归：
// A-1 session.status busy→running 执行态镜像（1.18.32 真实捕获件
// go-bridge/testdata/opencode-1.18.32-probe-20261001/ 驱动＋1.18.18 历史样本
// 同映射）＋别名幂等＋retry 零状态发射；A-3 sync 帧既有忽略的回归断言。
// 证据边界：1.18.18 fixture 是历史版本证据；1.18.32 捕获件是目标运行版本证据。

// captureDataLines extracts the `data: ` payload lines of one archived SSE
// capture file (physical SSE text, not JSONL despite the extension).
func captureDataLines(t *testing.T, rel string) []string {
	t.Helper()
	data, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read capture %s: %v", rel, err)
	}
	var frames []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "data: "); ok && after != "" {
			frames = append(frames, after)
		}
	}
	if len(frames) == 0 {
		t.Fatalf("capture %s carries no data frames", rel)
	}
	return frames
}

// official118SamplePayloads rewraps one archived 1.18.18 sample's direct
// payload frames into the envelope handleRawEvent consumes (same shape as
// a1SSEPayloads, generalized by file name).
func official118SamplePayloads(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/official-1.18.18/samples/" + name + ".sanitized.json")
	if err != nil {
		t.Fatalf("read %s sample: %v", name, err)
	}
	var doc struct {
		SSE []struct {
			Event struct {
				Payload map[string]any `json:"payload"`
			} `json:"event"`
		} `json:"sse"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s sample: %v", name, err)
	}
	if len(doc.SSE) == 0 {
		t.Fatalf("%s sample carries no SSE frames", name)
	}
	frames := make([]string, 0, len(doc.SSE))
	for _, f := range doc.SSE {
		b, err := json.Marshal(map[string]any{"payload": f.Event.Payload})
		if err != nil {
			t.Fatalf("rewrap frame: %v", err)
		}
		frames = append(frames, string(b))
	}
	return frames
}

// mirrorStates filters one drain into the mirrored execution-state sequence.
func mirrorStates(events []core.Event) []string {
	var states []string
	for _, ev := range events {
		if ev.Type == core.EventSessionState && ev.SessionState != nil {
			states = append(states, ev.SessionState.State)
		}
	}
	return states
}

func wantStates(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("mirror states = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mirror states = %v, want %v", got, want)
		}
	}
}

// TestSessionStatusMirrorFrom132Capture: the target-version capture carries
// busy→idle per turn window; the deprecated session.idle alias that follows
// status:idle must NOT double the mirror.
func TestSessionStatusMirrorFrom132Capture(t *testing.T) {
	for _, rel := range []string{
		"../../go-bridge/testdata/opencode-1.18.32-probe-20261001/sse-global.sse.jsonl",
		"../../go-bridge/testdata/opencode-1.18.32-probe-20261001/sse-global2.sse.jsonl",
	} {
		agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
		sub := newDrivenSubscriber(t, agent)
		driveFrames(sub, captureDataLines(t, rel)...)
		wantStates(t, mirrorStates(drain(sub)), "running", "idle")
	}
}

// TestSessionStatusMirrorBusyRepeatAndTwoRounds: repeated busy mirrors once;
// busy→idle→busy→idle fires every transition (the guard suppresses only
// unchanged states, never re-transitions).
func TestSessionStatusMirrorBusyRepeatAndTwoRounds(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)
	driveFrames(sub,
		sseFrame("session.status", map[string]any{"sessionID": "ses_m", "status": map[string]any{"type": "busy"}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_m", "status": map[string]any{"type": "busy"}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_m", "status": map[string]any{"type": "idle"}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_m", "status": map[string]any{"type": "busy"}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_m", "status": map[string]any{"type": "idle"}}),
	)
	wantStates(t, mirrorStates(drain(sub)), "running", "idle", "running", "idle")
}

// TestSessionIdleAliasMirrorsIdempotently: the alias alone still mirrors
// (alias-only server versions keep the state feed); paired with status:idle
// the two frames collapse to one mirror emission.
func TestSessionIdleAliasMirrorsIdempotently(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)
	driveFrames(sub, sseFrame("session.idle", map[string]any{"sessionID": "ses_a"}))
	wantStates(t, mirrorStates(drain(sub)), "idle")

	driveFrames(sub,
		sseFrame("session.status", map[string]any{"sessionID": "ses_b", "status": map[string]any{"type": "busy"}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_b", "status": map[string]any{"type": "idle"}}),
		sseFrame("session.idle", map[string]any{"sessionID": "ses_b"}),
	)
	wantStates(t, mirrorStates(drain(sub)), "running", "idle")
}

// TestRetryStatusEmitsNoMirrorState: retry is a transient in-turn state —
// mirroring it would flap the badge; the retry notice itself still emits.
func TestRetryStatusEmitsNoMirrorState(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)
	driveFrames(sub,
		sseFrame("session.status", map[string]any{"sessionID": "ses_r", "status": map[string]any{"type": "busy"}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_r", "status": map[string]any{
			"type": "retry", "attempt": 1, "message": "provider hiccup", "next": 1787109137538}}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_r", "status": map[string]any{
			"type": "retry", "attempt": 2, "message": "provider hiccup", "next": 1787109141613}}),
	)
	events := drain(sub)
	wantStates(t, mirrorStates(events), "running")
	retries := 0
	for _, ev := range events {
		if ev.Type == core.EventRetryStatus {
			retries++
		}
	}
	if retries == 0 {
		t.Fatal("retry notice must still emit EventRetryStatus")
	}
}

// TestA1FixtureMirrorRegression (1.18.18 historical evidence): busy×3 → one
// running mirror; idle＋alias → one idle mirror.
func TestA1FixtureMirrorRegression(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)
	driveFrames(sub, official118SamplePayloads(t, "a1-first-healthy-text")...)
	wantStates(t, mirrorStates(drain(sub)), "running", "idle")
}

// TestA3FixtureMirrorRegression (1.18.18 historical evidence): the provider
// error turn interleaves busy×3 / retry×2 / idle×2＋alias×2 — the mirror
// collapses to [running, idle] and retry contributes no state.
func TestA3FixtureMirrorRegression(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)
	driveFrames(sub, official118SamplePayloads(t, "a3-provider-error")...)
	events := drain(sub)
	wantStates(t, mirrorStates(events), "running", "idle")
	retries := 0
	for _, ev := range events {
		if ev.Type == core.EventRetryStatus {
			retries++
		}
	}
	if retries == 0 {
		t.Fatal("a3 retry frames must still emit EventRetryStatus")
	}
}

// TestSyncFramesFromCaptureEmitNothing (A-3): the five sync frames of the
// 1.18.32 capture ride the explicitly-ignored branch — zero emissions, no
// unknown-word handling, no state leakage.
func TestSyncFramesFromCaptureEmitNothing(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)
	var syncFrames []string
	for _, line := range captureDataLines(t, "../../go-bridge/testdata/opencode-1.18.32-probe-20261001/sse-global.sse.jsonl") {
		var probe struct {
			Payload struct {
				Type string `json:"type"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &probe) == nil && probe.Payload.Type == "sync" {
			syncFrames = append(syncFrames, line)
		}
	}
	if len(syncFrames) == 0 {
		t.Fatal("capture must carry sync frames for the A-3 assertion")
	}
	driveFrames(sub, syncFrames...)
	if events := drain(sub); len(events) != 0 {
		t.Fatalf("sync frames must emit nothing, got %d events", len(events))
	}
}
