// DSH SDK protocol connectivity probe (Gate 0 evidence).
//
// Proves a Go process can spawn the DSH JSON-RPC agent runtime over stdio,
// drive initialize + session/prompt, and consume the live session.event
// notification stream — without a real DEEPSEEK_API_KEY.
//
// Strategy mirrors DSH's own examples/jsonrpc-agent/tests/keyless-smoke.e2e.ts:
// a local HTTP server impersonates the DeepSeek chat-completions endpoint and
// streams a canned SSE turn; DEEPSEEK_BASE_URL points the runtime at it.
//
// Usage: DSH_ROOT=/path/to/deepseek-harness go run main.go
// (Requires DSH_ROOT with `pnpm install` done so tsx is available.)
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func repoRoot() string {
	r := os.Getenv("DSH_ROOT")
	if r == "" {
		fmt.Fprintln(os.Stderr, "FATAL: DSH_ROOT env var required (path to deepseek-harness checkout)")
		os.Exit(2)
	}
	return r
}

type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func main() {
	root := repoRoot()
	binTS := root + "/packages/examples/jsonrpc-demo/src/bin.ts"
	cfgYML := root + "/examples/jsonrpc-agent/cordis.yml"

	// 1. mock DeepSeek chat-completions server (canned normal-completion turn)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	port := ln.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	modelHits := 0
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			modelHits++
			fmt.Printf("[mock] #%d %s %s  body=%d bytes\n", modelHits, r.Method, r.URL.Path, len(body))
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fl, _ := w.(http.Flusher)
			sse := func(s string) { fmt.Fprintf(w, "data: %s\n\n", s); fl.Flush() }
			sse(`{"choices":[{"delta":{"role":"assistant","content":null}}]}`)
			sse(`{"choices":[{"delta":{"content":"Hello from DSH mock"}}]}`)
			sse(`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":15,"completion_tokens":4}}`)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			fl.Flush()
		}))
	}()

	// 2. temp workspace + session root
	cwd, err := os.MkdirTemp("", "dsh-conn-cwd-")
	must(err)
	sessionRoot := cwd + "/.sessions"
	_ = os.MkdirAll(sessionRoot, 0o755)

	// 3. spawn node --import tsx bin.ts cordis.yml
	cmd := exec.Command("node", "--import", "tsx", binTS, cfgYML)
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"USER=" + os.Getenv("USER"),
		"LANG=" + os.Getenv("LANG"),
		"DEEPSEEK_API_KEY=dsh-conn-fake-key",
		"DEEPSEEK_BASE_URL=" + baseURL,
		"DSH_CWD=" + cwd,
		"DSH_SESSION_ROOT=" + sessionRoot,
	}
	stdin, err := cmd.StdinPipe()
	must(err)
	stdout, err := cmd.StdoutPipe()
	must(err)
	stderrPipe, err := cmd.StderrPipe()
	must(err)
	must(cmd.Start())
	fmt.Printf("[spawn] pid=%d\n", cmd.Process.Pid)

	var stderrBuf strings.Builder
	go io.Copy(io.MultiWriter(&stderrBuf, os.Stderr), stderrPipe)

	// 4. stdout line dispatcher
	lines := make(chan string, 4096)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	var mu sync.Mutex
	pending := map[string]chan *rpcFrame{}
	var eventTypes []string
	var sampleEvent *rpcFrame
	regPending := func(key string) chan *rpcFrame {
		ch := make(chan *rpcFrame, 1)
		mu.Lock()
		pending[key] = ch
		mu.Unlock()
		return ch
	}
	takePending := func(key string) chan *rpcFrame {
		mu.Lock()
		ch := pending[key]
		delete(pending, key)
		mu.Unlock()
		return ch
	}
	go func() {
		for line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var f rpcFrame
			if err := json.Unmarshal([]byte(line), &f); err != nil {
				fmt.Printf("[non-json stdout] %s\n", truncate(line, 300))
				continue
			}
			if len(f.ID) > 0 && f.Method == "" {
				if ch := takePending(string(f.ID)); ch != nil {
					ch <- &f
				}
				continue
			}
			if f.Method == "session.event" {
				var p struct {
					SessionID string `json:"sessionId"`
					Event     struct {
						Type string `json:"type"`
						Seq  int    `json:"seq"`
					} `json:"event"`
				}
				_ = json.Unmarshal(f.Params, &p)
				mu.Lock()
				eventTypes = append(eventTypes, p.Event.Type)
				if sampleEvent == nil {
					cp := f
					sampleEvent = &cp
				}
				mu.Unlock()
				fmt.Printf("[event] seq=%-3d type=%s\n", p.Event.Seq, p.Event.Type)
			} else if f.Method == "session.status" {
				fmt.Printf("[status] %s\n", truncate(string(f.Params), 200))
			} else if f.Method != "" {
				fmt.Printf("[notif] method=%s\n", f.Method)
			}
		}
	}()

	send := func(id int, method string, params any) {
		obj := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
		if params != nil {
			obj["params"] = params
		}
		b, _ := json.Marshal(obj)
		_, _ = stdin.Write(append(b, '\n'))
	}
	waitResp := func(id int) *rpcFrame {
		ch := regPending(fmt.Sprintf("%d", id))
		defer func() { _ = takePending(fmt.Sprintf("%d", id)) }()
		select {
		case f := <-ch:
			return f
		case <-time.After(45 * time.Second):
			fmt.Printf("[TIMEOUT] waiting id=%d\n", id)
			return nil
		}
	}
	hasEventType := func(t string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range eventTypes {
			if e == t {
				return true
			}
		}
		return false
	}

	send(1, "initialize", map[string]any{"cwd": cwd, "provider": "deepseek-official", "model": "deepseek-chat", "maxTokens": 256})
	if r := waitResp(1); r != nil {
		fmt.Printf("[initialize OK] %s\n", truncate(string(r.Result), 300))
	} else {
		fatal("initialize timed out")
	}
	send(2, "session/prompt", map[string]any{"sessionId": "conn-test", "contentBlocks": []map[string]any{{"type": "text", "text": "say hello"}}})
	if r := waitResp(2); r != nil {
		fmt.Printf("[prompt OK] %s\n", truncate(string(r.Result), 200))
	} else {
		fatal("session/prompt timed out")
	}
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) && !hasEventType("turn/end") {
		time.Sleep(100 * time.Millisecond)
	}
	send(3, "shutdown", nil)
	if r := waitResp(3); r != nil {
		fmt.Printf("[shutdown OK] %s\n", truncate(string(r.Result), 80))
	}
	_ = stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}

	mu.Lock()
	defer mu.Unlock()
	fmt.Println("\n==================== RESULT ====================")
	fmt.Printf("captured %d session.event notifications:\n", len(eventTypes))
	for _, t := range eventTypes {
		fmt.Printf("  - %s\n", t)
	}
	turnedOK := false
	for _, t := range eventTypes {
		if t == "turn/end" {
			turnedOK = true
		}
	}
	if turnedOK {
		fmt.Println("\nVERDICT: PASS — Go drove DSH over stdio JSON-RPC and received the live session.event stream.")
	} else {
		fmt.Println("\nVERDICT: PARTIAL — handshake/prompt ok but no turn/end captured.")
	}
}

func must(err error) {
	if err != nil {
		fatal(err.Error())
	}
}
func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "FATAL:", msg)
	os.Exit(1)
}
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
