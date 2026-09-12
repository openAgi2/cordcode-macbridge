package gobridge

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

// runtimeDiagnosticStat aggregates one privacy-safe dimension. It deliberately
// stores no session ids, user paths, prompts, model output, or error text: the
// keys are backend/event/outcome class only.
type runtimeDiagnosticStat struct {
	Count        uint64 `json:"count"`
	TotalMillis  uint64 `json:"totalMillis"`
	MaxMillis    uint64 `json:"maxMillis"`
	LastMillis   uint64 `json:"lastMillis"`
	P50Millis    uint64 `json:"p50Millis"`
	P95Millis    uint64 `json:"p95Millis"`
	LastObserved string `json:"lastObserved,omitempty"`
	recentMillis []uint64
}

type runtimeDiagnostics struct {
	mu      sync.Mutex
	started time.Time

	passive       map[string]*runtimeDiagnosticStat
	background    map[string]*runtimeDiagnosticStat
	discovery     map[string]*runtimeDiagnosticStat
	backgroundNow map[string]uint64
}

func newRuntimeDiagnostics() *runtimeDiagnostics {
	return &runtimeDiagnostics{
		started:       time.Now(),
		passive:       make(map[string]*runtimeDiagnosticStat),
		background:    make(map[string]*runtimeDiagnosticStat),
		discovery:     make(map[string]*runtimeDiagnosticStat),
		backgroundNow: make(map[string]uint64),
	}
}

func (d *runtimeDiagnostics) observePassive(backendID, eventName string) {
	if d == nil {
		return
	}
	d.observe("passive:"+backendID+"|"+eventName, 0, d.passive)
}

func (d *runtimeDiagnostics) beginBackgroundTask(backendID string) func(outcome string) {
	if d == nil {
		return func(string) {}
	}
	started := time.Now()
	d.mu.Lock()
	d.backgroundNow[backendID]++
	d.mu.Unlock()
	return func(outcome string) {
		duration := time.Since(started)
		d.mu.Lock()
		if d.backgroundNow[backendID] > 0 {
			d.backgroundNow[backendID]--
		}
		d.mu.Unlock()
		d.observe("background:"+backendID+"|"+outcome, duration, d.background)
	}
}

func (d *runtimeDiagnostics) observeDiscovery(backendID, phase, outcome string, duration time.Duration) {
	if d == nil {
		return
	}
	d.observe("discovery:"+backendID+"|"+phase+"|"+outcome, duration, d.discovery)
}

func (d *runtimeDiagnostics) observe(key string, duration time.Duration, into map[string]*runtimeDiagnosticStat) {
	millis := uint64(duration.Milliseconds())
	now := time.Now().UTC().Format(time.RFC3339)
	d.mu.Lock()
	defer d.mu.Unlock()
	stat, ok := into[key]
	if !ok {
		stat = &runtimeDiagnosticStat{}
		into[key] = stat
	}
	stat.Count++
	stat.TotalMillis += millis
	stat.LastMillis = millis
	stat.LastObserved = now
	if millis > stat.MaxMillis {
		stat.MaxMillis = millis
	}
	stat.recentMillis = append(stat.recentMillis, millis)
	if len(stat.recentMillis) > 128 {
		stat.recentMillis = append([]uint64(nil), stat.recentMillis[len(stat.recentMillis)-128:]...)
	}
}

func (d *runtimeDiagnostics) snapshot() map[string]interface{} {
	if d == nil {
		return map[string]interface{}{}
	}
	d.mu.Lock()
	passive := cloneRuntimeStats(d.passive)
	background := cloneRuntimeStats(d.background)
	discovery := cloneRuntimeStats(d.discovery)
	activeBackground := make(map[string]uint64, len(d.backgroundNow))
	for k, v := range d.backgroundNow {
		activeBackground[k] = v
	}
	d.mu.Unlock()
	userCPU, systemCPU, cpuOK := processCPUSeconds()
	result := map[string]interface{}{
		"schemaVersion":           1,
		"startedAt":               d.started.UTC().Format(time.RFC3339),
		"goroutineCount":          runtime.NumGoroutine(),
		"processCPUAvailable":     cpuOK,
		"processUserCPUSeconds":   userCPU,
		"processSystemCPUSeconds": systemCPU,
		"passiveEvents":           passive,
		"backgroundTasks":         background,
		"activeBackgroundScans":   activeBackground,
		"discovery":               discovery,
	}
	return result
}

func cloneRuntimeStats(src map[string]*runtimeDiagnosticStat) map[string]*runtimeDiagnosticStat {
	out := make(map[string]*runtimeDiagnosticStat, len(src))
	for key, stat := range src {
		copied := *stat
		copied.recentMillis = append([]uint64(nil), stat.recentMillis...)
		sort.Slice(copied.recentMillis, func(i, j int) bool { return copied.recentMillis[i] < copied.recentMillis[j] })
		copied.P50Millis = percentileMillis(copied.recentMillis, 50)
		copied.P95Millis = percentileMillis(copied.recentMillis, 95)
		// Recent samples are summarized by p50/p95; retain no raw series in snapshots.
		copied.recentMillis = nil
		out[key] = &copied
	}
	return out
}

// backgroundTaskScanMetricsProvider is an optional, dependency-cycle-free
// agent seam. Providers return aggregate counters only; no transcript content
// or stable session identity crosses the seam.
type backgroundTaskScanMetricsProvider interface {
	BackgroundTaskScanCounters() (
		active uint64,
		scans uint64,
		successes uint64,
		failures uint64,
		turnItemRequests uint64,
		scannedTurns uint64,
		lastDurationMillis int64,
	)
}

func percentileMillis(samples []uint64, percentile int) uint64 {
	if len(samples) == 0 {
		return 0
	}
	index := (percentile*len(samples) + 99) / 100
	if index <= 0 {
		index = 1
	}
	if index > len(samples) {
		index = len(samples)
	}
	return samples[index-1]
}
