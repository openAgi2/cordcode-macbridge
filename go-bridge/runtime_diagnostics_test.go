package gobridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestRuntimeDiagnosticsAggregatesPrivacySafeCategories(t *testing.T) {
	diagnostics := newRuntimeDiagnostics()
	diagnostics.observePassive("codex-remote", "error")
	diagnostics.observeDiscovery("codex-remote", "poll", "timeout", 125*time.Millisecond)

	finish := diagnostics.beginBackgroundTask("codex-remote")
	snapshot := diagnostics.snapshot()
	active := snapshot["activeBackgroundScans"].(map[string]uint64)
	if active["codex-remote"] != 1 {
		t.Fatalf("active background scans = %v", active)
	}
	finish("success")

	snapshot = diagnostics.snapshot()
	passive := snapshot["passiveEvents"].(map[string]*runtimeDiagnosticStat)
	if got := passive["passive:codex-remote|error"]; got == nil || got.Count != 1 {
		t.Fatalf("passive stat = %+v", got)
	}
	discovery := snapshot["discovery"].(map[string]*runtimeDiagnosticStat)
	if got := discovery["discovery:codex-remote|poll|timeout"]; got == nil || got.Count != 1 || got.LastMillis != 125 {
		t.Fatalf("discovery stat = %+v", got)
	}
	background := snapshot["backgroundTasks"].(map[string]*runtimeDiagnosticStat)
	if got := background["background:codex-remote|success"]; got == nil || got.Count != 1 {
		t.Fatalf("background stat = %+v", got)
	}
	active = snapshot["activeBackgroundScans"].(map[string]uint64)
	if active["codex-remote"] != 0 {
		t.Fatalf("background scan did not finish: %v", active)
	}
	if _, ok := snapshot["goroutineCount"]; !ok {
		t.Fatal("goroutine count missing")
	}
	// Exercise a multi-sample latency distribution at the management shape boundary.
	for i := 0; i < 100; i++ {
		diagnostics.observeDiscovery("codex-remote", "poll", "ok", time.Duration(i+1)*time.Millisecond)
	}
	got := diagnostics.snapshot()["discovery"].(map[string]*runtimeDiagnosticStat)["discovery:codex-remote|poll|ok"]
	if got.P50Millis != 50 || got.P95Millis != 95 {
		t.Fatalf("p50/p95 = %d/%d", got.P50Millis, got.P95Millis)
	}
}

type diagnosticsMetricsAgent struct {
	mgmtFakeAgent
}

func (a *diagnosticsMetricsAgent) BackgroundTaskScanCounters() (active, scans, successes, failures, turnItemRequests, scannedTurns uint64, lastDurationMillis int64) {
	return 1, 3, 2, 1, 12, 30, 1250
}

func TestManagementRuntimeDiagnosticsEndpoint(t *testing.T) {
	srv := newTestMgmtServer(map[string]core.Agent{
		"codex-remote": &diagnosticsMetricsAgent{mgmtFakeAgent: mgmtFakeAgent{name: "codex-remote"}},
	})
	srv.cfg.Handlers.runtimeDiagnostics.observePassive("codex-remote", "error")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authRequest(http.MethodGet, "/internal/diagnostics/runtime"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var decoded struct {
		SchemaVersion  int                               `json:"schemaVersion"`
		GoroutineCount int                               `json:"goroutineCount"`
		PassiveEvents  map[string]*runtimeDiagnosticStat `json:"passiveEvents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != 1 || decoded.GoroutineCount <= 0 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if got := decoded.PassiveEvents["passive:codex-remote|error"]; got == nil || got.Count != 1 {
		t.Fatalf("passive stat = %+v", got)
	}

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	agentStatsAny, ok := raw["agentBackgroundScans:codex-remote"]
	if !ok {
		t.Fatalf("agent stats missing: %+v", raw)
	}
	agentStats, ok := agentStatsAny.(map[string]any)
	if !ok {
		t.Fatalf("agent stats type = %T", agentStatsAny)
	}
	if agentStats["activeScans"] != float64(1) || agentStats["scans"] != float64(3) ||
		agentStats["turnItemRequests"] != float64(12) || agentStats["scannedTurns"] != float64(30) {
		t.Fatalf("agent stats = %+v", agentStats)
	}
}
