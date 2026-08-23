//go:build liveprobe

package codexweb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestLiveProbeDaemonDump 连接当前官方 codex app-server daemon（与生产同链路：
// lifecycle Probe → OpenClient），原样 dump initialize / thread/list /
// thread/loaded/list 的真实 wire 形状；CODEXWEB_PROBE_THREAD 非空时再 resume 并
// 监听 120s 的全量 notification。仅诊断用，运行前需 CODEXWEB_LIVE_PROBE=1。
//
// 用法:
//
//	go test ./agent/codex-web/ -tags liveprobe -run TestLiveProbeDaemonDump -v -count=1
func TestLiveProbeDaemonDump(t *testing.T) {
	if os.Getenv("CODEXWEB_LIVE_PROBE") == "" {
		t.Skip("set CODEXWEB_LIVE_PROBE=1")
	}
	ctx := context.Background()
	a := &Agent{workDir: "/Users/jacklee"}
	ep, err := a.probeEndpoint()
	if err != nil {
		t.Fatalf("probeEndpoint: %v", err)
	}
	defer ep.Close()
	t.Logf("endpoint source=%s unix=%q tcp=%q cli=%q app=%q",
		ep.Source, ep.UnixSocket, ep.TCPEndpoint, ep.CLIVersion, ep.AppServerVersion)

	cl, err := ep.OpenClient(ctx, a.probeOptions())
	if err != nil {
		t.Fatalf("OpenClient: %v", err)
	}
	defer cl.Close()

	raw, rpcErr, err := cl.RequestContext(ctx, "thread/list", map[string]any{"limit": 50})
	if err != nil || rpcErr != nil {
		t.Fatalf("thread/list err=%v rpcErr=%#v", err, rpcErr)
	}
	fmt.Printf("THREAD_LIST_RAW=%s\n", raw)

	raw, rpcErr, err = cl.RequestContext(ctx, "thread/loaded/list", map[string]any{})
	if err != nil || rpcErr != nil {
		t.Fatalf("thread/loaded/list err=%v rpcErr=%#v", err, rpcErr)
	}
	fmt.Printf("LOADED_LIST_RAW=%s\n", raw)

	thread := os.Getenv("CODEXWEB_PROBE_THREAD")
	if thread == "" {
		fmt.Println("PROBE_DONE_NO_LISTEN")
		return
	}
	raw, rpcErr, err = cl.RequestContext(ctx, "thread/resume", map[string]string{"threadId": thread})
	fmt.Printf("RESUME_RAW=%s err=%v rpcErr=%#v\n", raw, err, rpcErr)

	deadline := time.NewTimer(120 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case n, ok := <-cl.Notifications():
			if !ok {
				fmt.Println("NOTIF_CHANNEL_CLOSED")
				return
			}
			fmt.Printf("NOTIF=%s PARAMS=%s\n", n.Method, string(n.Params))
		case <-deadline.C:
			fmt.Println("PROBE_DONE_TIMEOUT")
			return
		}
	}
}
