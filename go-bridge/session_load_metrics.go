package gobridge

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cccode-macbridge/core"
)

var sessionLoadTraceSequence atomic.Uint64

type sessionLoadRequestMetrics struct {
	traceID     string
	requestID   string
	method      string
	backendID   string
	route       string
	started     time.Time
	core        *core.SessionLoadMetrics
	wireMapping time.Duration
	jsonEncode  time.Duration
	socketSend  time.Duration
	responseLen int
	resultCount int
}

func newSessionLoadRequestMetrics(conn Connection, msg WireMessage) *sessionLoadRequestMetrics {
	return &sessionLoadRequestMetrics{
		traceID:   fmt.Sprintf("session-load-%d-%d", time.Now().UnixMilli(), sessionLoadTraceSequence.Add(1)),
		requestID: msg.RequestID,
		method:    msg.Method,
		backendID: msg.BackendID,
		route:     sessionLoadTransportRoute(conn),
		started:   time.Now(),
		core:      &core.SessionLoadMetrics{},
	}
}

func sessionLoadTransportRoute(conn Connection) string {
	if _, ok := conn.(*RelayDeviceConn); ok {
		return "relay"
	}
	return "direct"
}

func (m *sessionLoadRequestMetrics) context() *core.SessionLoadMetrics {
	if m == nil {
		return nil
	}
	return m.core
}

func (m *sessionLoadRequestMetrics) sendResult(conn Connection, requestID string, data interface{}, wireErr *WireError) {
	response := map[string]interface{}{
		"type":      "result",
		"requestId": requestID,
	}
	if wireErr != nil {
		response["ok"] = false
		response["error"] = wireErr
	} else {
		response["ok"] = true
		response["data"] = data
	}

	encodeStarted := time.Now()
	encoded, encodeErr := json.Marshal(response)
	m.jsonEncode += time.Since(encodeStarted)
	if encodeErr == nil {
		m.responseLen = len(encoded)
	}

	sendStarted := time.Now()
	conn.SendJSON(response)
	m.socketSend += time.Since(sendStarted)
	m.log(wireErr)
}

func (m *sessionLoadRequestMetrics) log(wireErr *WireError) {
	snapshot := m.core.Snapshot()
	var errorCode string
	if wireErr != nil {
		errorCode = wireErr.Code
	}
	slog.Info("go-bridge: session loading metrics",
		"trace_id", m.traceID,
		"request_id", m.requestID,
		"method", m.method,
		"backend_id", m.backendID,
		"transport_route", m.route,
		"request_total_ms", durationMillis(time.Since(m.started)),
		"enumerate_ms", durationMillis(snapshot.Enumerate),
		"stat_compare_ms", durationMillis(snapshot.StatCompare),
		"metadata_parse_ms", durationMillis(snapshot.MetadataParse),
		"history_parse_ms", durationMillis(snapshot.HistoryParse),
		"wire_mapping_ms", durationMillis(m.wireMapping),
		"json_encode_ms", durationMillis(m.jsonEncode),
		"socket_send_ms", durationMillis(m.socketSend),
		"response_bytes", m.responseLen,
		"cache_total_files", snapshot.CacheTotalFiles,
		"cache_changed_files", snapshot.CacheChanged,
		"cache_deleted_files", snapshot.CacheDeleted,
		"cache_hit", snapshot.CacheHit,
		"dataset_bytes", snapshot.DatasetBytes,
		"max_file_bytes", snapshot.MaxFileBytes,
		"result_count", m.resultCount,
		"error_code", errorCode)
}

func durationMillis(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}
