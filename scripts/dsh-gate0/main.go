// DSH SDK protocol connectivity probe (Gate 0 evidence).
//
// Two modes, selected by DEEPSEEK_API_KEY:
//   - mock  (key == "dsh-conn-fake-key"): local HTTP server impersonates the
//     DeepSeek API with a canned SSE turn — no real key needed (Gate 0).
//   - real  (any other non-empty key):    connects to the real DeepSeek API
//     (DEEPSEEK_BASE_URL optional), drives one turn with prompt = args[1],
//     and appends every session.event's full JSON params to DSH_GATE0_DUMP.
//
// Env:
//   DSH_ROOT            (required) path to deepseek-harness checkout (pnpm install done)
//   DEEPSEEK_API_KEY    fake key → mock; real key → real API
//   DSH_GATE0_CONFIG    (optional) cordis.yml path; default <DSH_ROOT>/examples/jsonrpc-agent/cordis.yml
//   DSH_GATE0_MAX_TOKENS (optional, real mode) initialize maxTokens; default 2048
//   DSH_GATE0_DUMP      (optional, real mode) file to append full session.event JSON
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
	"strconv"
	"strings"
	"sync"
	"time"
)

func repoRoot() string {
	r := os.Getenv("DSH_ROOT")
	if r == "" {
		fmt.Fprintln(os.Stderr, "FATAL: DSH_ROOT env var required")
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
	cfgYML := os.Getenv("DSH_GATE0_CONFIG")
	if cfgYML == "" {
		cfgYML = root + "/examples/jsonrpc-agent/cordis.yml"
	}

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	realMode := apiKey != "" && apiKey != "dsh-conn-fake-key"
	var baseURL, promptText string
	var maxTokens int = 2048
	if v := os.Getenv("DSH_GATE0_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxTokens = n
		}
	}
	var dumpFile *os.File
	if realMode {
		if len(os.Args) < 2 {
			fatal("real mode requires prompt as args[1]")
		}
		promptText = os.Args[1]
		baseURL = os.Getenv("DEEPSEEK_BASE_URL") // empty → runtime default
		if dp := os.Getenv("DSH_GATE0_DUMP"); dp != "" {
			f, err := os.OpenFile(dp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			must(err)
			dumpFile = f
			defer dumpFile.Close()
		}
		fmt.Printf("[mode] REAL  prompt=%q maxTokens=%d config=%s\n", promptText, maxTokens, cfgYML)
	} else {
		apiKey = "dsh-conn-fake-key"
		promptText = "say hello"
		// mock server
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		must(err)
		port := ln.Addr().(*net.TCPAddr).Port
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
		go func() {
			_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.ReadAll(r.Body)
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
		fmt.Printf("[mode] MOCK (Gate 0)\n")
	}

	cwd, err := os.MkdirTemp("", "dsh-conn-cwd-")
	must(err)
	sessionRoot := cwd + "/.sessions"
	_ = os.MkdirAll(sessionRoot, 0o755)

	cmd := exec.Command("node", "--import", "tsx", binTS, cfgYML)
	cmd.Dir = root
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"USER=" + os.Getenv("USER"),
		"LANG=" + os.Getenv("LANG"),
		"DEEPSEEK_API_KEY=" + apiKey,
		"DSH_CWD=" + cwd,
		"DSH_SESSION_ROOT=" + sessionRoot,
	}
	if baseURL != "" {
		env = append(env, "DEEPSEEK_BASE_URL="+baseURL)
	}
	cmd.Env = env
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
					Event struct {
						Type string `json:"type"`
						Seq  int    `json:"seq"`
					} `json:"event"`
				}
				_ = json.Unmarshal(f.Params, &p)
				mu.Lock()
				eventTypes = append(eventTypes, p.Event.Type)
				mu.Unlock()
				fmt.Printf("[event] seq=%-3d type=%s\n", p.Event.Seq, p.Event.Type)
				if dumpFile != nil {
					mu.Lock()
					dumpFile.Write(append(f.Params, '\n'))
					mu.Unlock()
				}
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
		case <-time.After(90 * time.Second):
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

	send(1, "initialize", map[string]any{"cwd": cwd, "provider": "deepseek-official", "model": "deepseek-chat", "maxTokens": maxTokens})
	if r := waitResp(1); r != nil {
		fmt.Printf("[initialize OK] %s\n", truncate(string(r.Result), 300))
	} else {
		fatal("initialize timed out")
	}
	send(2, "session/prompt", map[string]any{"sessionId": "conn-test", "contentBlocks": []map[string]any{{"type": "text", "text": promptText}}})
	if r := waitResp(2); r != nil {
		fmt.Printf("[prompt OK] %s\n", truncate(string(r.Result), 200))
	} else {
		fatal("session/prompt timed out")
	}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) && !hasEventType("turn/end") {
		time.Sleep(150 * time.Millisecond)
	}
	send(3, "shutdown", nil)
	waitResp(3)
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
	// distinct types in order
	seen := map[string]bool{}
	var distinct []string
	turnedOK := false
	for _, t := range eventTypes {
		if t == "turn/end" {
			turnedOK = true
		}
		if !seen[t] {
			seen[t] = true
			distinct = append(distinct, t)
		}
	}
	fmt.Printf("captured %d session.event (%d distinct types):\n", len(eventTypes), len(distinct))
	for _, t := range distinct {
		fmt.Printf("  - %s\n", t)
	}
	if turnedOK {
		fmt.Println("\nVERDICT: PASS — turn/end captured.")
	} else {
		fmt.Println("\nVERDICT: PARTIAL — no turn/end.")
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
