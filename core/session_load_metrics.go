package core

import (
	"context"
	"sync"
	"time"
)

// SessionLoadMetrics collects request-scoped measurements without coupling
// agents to the bridge logger or transport implementation.
type SessionLoadMetrics struct {
	mu sync.Mutex

	Enumerate       time.Duration
	StatCompare     time.Duration
	MetadataParse   time.Duration
	HistoryParse    time.Duration
	CacheTotalFiles int
	CacheChanged    int
	CacheDeleted    int
	CacheHit        bool
	DatasetBytes    int64
	MaxFileBytes    int64
}

type SessionLoadMetricsSnapshot struct {
	Enumerate       time.Duration
	StatCompare     time.Duration
	MetadataParse   time.Duration
	HistoryParse    time.Duration
	CacheTotalFiles int
	CacheChanged    int
	CacheDeleted    int
	CacheHit        bool
	DatasetBytes    int64
	MaxFileBytes    int64
}

func (m *SessionLoadMetrics) RecordEnumeration(elapsed time.Duration, files int, totalBytes, maxFileBytes int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.Enumerate += elapsed
	m.CacheTotalFiles += files
	m.DatasetBytes += totalBytes
	if maxFileBytes > m.MaxFileBytes {
		m.MaxFileBytes = maxFileBytes
	}
	m.mu.Unlock()
}

func (m *SessionLoadMetrics) RecordStatCompare(elapsed time.Duration, changed, deleted int, hit bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.StatCompare += elapsed
	m.CacheChanged += changed
	m.CacheDeleted += deleted
	m.CacheHit = hit
	m.mu.Unlock()
}

func (m *SessionLoadMetrics) AddMetadataParse(elapsed time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.MetadataParse += elapsed
	m.mu.Unlock()
}

func (m *SessionLoadMetrics) AddHistoryParse(elapsed time.Duration, fileBytes int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.HistoryParse += elapsed
	if fileBytes > 0 {
		m.DatasetBytes += fileBytes
		if fileBytes > m.MaxFileBytes {
			m.MaxFileBytes = fileBytes
		}
	}
	m.mu.Unlock()
}

func (m *SessionLoadMetrics) Snapshot() SessionLoadMetricsSnapshot {
	if m == nil {
		return SessionLoadMetricsSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return SessionLoadMetricsSnapshot{
		Enumerate:       m.Enumerate,
		StatCompare:     m.StatCompare,
		MetadataParse:   m.MetadataParse,
		HistoryParse:    m.HistoryParse,
		CacheTotalFiles: m.CacheTotalFiles,
		CacheChanged:    m.CacheChanged,
		CacheDeleted:    m.CacheDeleted,
		CacheHit:        m.CacheHit,
		DatasetBytes:    m.DatasetBytes,
		MaxFileBytes:    m.MaxFileBytes,
	}
}

type sessionLoadMetricsContextKey struct{}

func WithSessionLoadMetrics(ctx context.Context, metrics *SessionLoadMetrics) context.Context {
	return context.WithValue(ctx, sessionLoadMetricsContextKey{}, metrics)
}

func SessionLoadMetricsFromContext(ctx context.Context) *SessionLoadMetrics {
	if ctx == nil {
		return nil
	}
	metrics, _ := ctx.Value(sessionLoadMetricsContextKey{}).(*SessionLoadMetrics)
	return metrics
}
