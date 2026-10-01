package opencodeweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// sse_watchdog_test.go —— 2026-10-01 owner 矩阵 r1 根因修复的定向测试：
// 长连 SSE 被服务器侧静默孤儿化（连接 ESTABLISHED、零帧、无错误）时，心跳
// 看门狗必须在 deadline 内主动断开并走既有 heal+reconnect；正常心跳节律下
// 看门狗不得误杀。

// watchdogSSEServe serves a /global/event stream that either stays silent
// (orphan simulation) or emits a heartbeat frame every beat (healthy stream).
func watchdogSSEServe(t *testing.T, silent bool, beat time.Duration) (*httptest.Server, *int32) {
	t.Helper()
	var dials int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case "/session":
			_, _ = w.Write([]byte(`[]`))
		case "/agent":
			_, _ = w.Write([]byte(`[{"name":"build","mode":"primary","native":true,"description":"general coding"}]`))
		case "/provider":
			_, _ = w.Write([]byte(testProviderCatalog))
		case "/global/event":
			atomic.AddInt32(&dials, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			if silent {
				<-r.Context().Done()
				return
			}
			ticker := time.NewTicker(beat)
			defer ticker.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case <-ticker.C:
					if _, err := w.Write([]byte("data: {\"payload\":{\"type\":\"server.heartbeat\",\"properties\":{}}}\n\n")); err != nil {
						return
					}
					w.(http.Flusher).Flush()
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &dials
}

func withWatchdog(t *testing.T, d time.Duration) {
	t.Helper()
	orig := sseHeartbeatWatchdog
	sseHeartbeatWatchdog = d
	t.Cleanup(func() { sseHeartbeatWatchdog = orig })
}

// TestSSEWatchdogReconnectsSilentOrphan: the server accepts the SSE dial then
// never writes a byte — the pre-fix reader blocks forever (owner r1: ESTABLISHED
// connection, empty queues, zero frames for hours). The watchdog must close the
// orphan and redial (heal path), visibly growing the dial count.
func TestSSEWatchdogReconnectsSilentOrphan(t *testing.T) {
	withWatchdog(t, 300*time.Millisecond)
	srv, dials := watchdogSSEServe(t, true, 0)
	agent := agentForSSE(t, srv.URL)

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()
	if _, err := agent.Subscribe(pctx); err != nil {
		t.Fatalf("passive subscribe: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	for atomicLoadInt32(dials) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := atomicLoadInt32(dials); got < 2 {
		t.Fatalf("watchdog must close the silent orphan and redial; dials=%d", got)
	}
}

// TestSSEWatchdogKeepsHealthyHeartbeatAlive: with heartbeats flowing well
// inside the deadline, the stream must stay connected — the watchdog must not
// fire on a healthy connection.
func TestSSEWatchdogKeepsHealthyHeartbeatAlive(t *testing.T) {
	withWatchdog(t, 500*time.Millisecond)
	srv, dials := watchdogSSEServe(t, false, 100*time.Millisecond)
	agent := agentForSSE(t, srv.URL)

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()
	if _, err := agent.Subscribe(pctx); err != nil {
		t.Fatalf("passive subscribe: %v", err)
	}

	time.Sleep(1600 * time.Millisecond)
	if got := atomicLoadInt32(dials); got != 1 {
		t.Fatalf("healthy heartbeat stream must stay on one connection, dials=%d", got)
	}
}
