package core

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSessionLoadMetricsAggregatesConcurrentStages(t *testing.T) {
	metrics := &SessionLoadMetrics{}
	ctx := WithSessionLoadMetrics(context.Background(), metrics)
	if SessionLoadMetricsFromContext(ctx) != metrics {
		t.Fatal("metrics collector was not preserved in context")
	}

	var waitGroup sync.WaitGroup
	for range 4 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			metrics.RecordEnumeration(time.Millisecond, 2, 100, 60)
			metrics.RecordStatCompare(2*time.Millisecond, 1, 1, false)
			metrics.AddMetadataParse(3 * time.Millisecond)
			metrics.AddHistoryParse(4*time.Millisecond, 80)
		}()
	}
	waitGroup.Wait()

	snapshot := metrics.Snapshot()
	if snapshot.Enumerate != 4*time.Millisecond ||
		snapshot.StatCompare != 8*time.Millisecond ||
		snapshot.MetadataParse != 12*time.Millisecond ||
		snapshot.HistoryParse != 16*time.Millisecond {
		t.Fatalf("unexpected duration totals: %+v", snapshot)
	}
	if snapshot.CacheTotalFiles != 8 || snapshot.CacheChanged != 4 || snapshot.CacheDeleted != 4 {
		t.Fatalf("unexpected cache totals: %+v", snapshot)
	}
	if snapshot.DatasetBytes != 720 || snapshot.MaxFileBytes != 80 {
		t.Fatalf("unexpected dataset totals: %+v", snapshot)
	}
}
