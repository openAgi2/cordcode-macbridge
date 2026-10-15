package codexremote

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// chatGPTCodexCandidates mirrors the official ChatGPT Desktop codex CLI
// locations, newest layout first. ChatGPT 26.924.20706 (codex 0.158.0-alpha.2)
// replaced the flat Resources/codex binary with the install-context
// layoutVersion 1 package (codex-package.json + bin/codex wrapper +
// CodexCLI.app/Contents/MacOS/codex); older releases keep the flat binary.
func chatGPTCodexCandidates() []string {
	return []string{
		"/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex",
		"/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex",
		"/Applications/ChatGPT.app/Contents/Resources/codex",
	}
}

func resolveChatGPTCodexPath(exists func(string) bool) string {
	candidates := chatGPTCodexCandidates()
	for _, p := range candidates {
		if exists(p) {
			return p
		}
	}
	// None installed: keep the legacy path so os.Stat in loadChatGPTAuth
	// still produces the "请先安装并登录 ChatGPT Desktop" guidance.
	return candidates[len(candidates)-1]
}

func defaultChatGPTCodexPath() string {
	return resolveChatGPTCodexPath(func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
}

// ResolveChatGPTCodexInstallPath is read-only workspace inventory. It does not run
// app-server or the Remote Control auth probe.
func ResolveChatGPTCodexInstallPath() string {
	return defaultChatGPTCodexPath()
}

// chatGPTCodexBin resolves the probe binary loadChatGPTAuth execs. It is a
// package-level var so tests can point the auth probe at a stub binary
// (zombie-reaping regression, incidents/2026-10-12-bridge-zombie-process-
// exhaustion.md §5.1).
var chatGPTCodexBin = defaultChatGPTCodexPath

func loadChatGPTAuth(ctx context.Context) (token, accountID string, err error) {
	bin := chatGPTCodexBin()
	if _, statErr := os.Stat(bin); statErr != nil {
		return "", "", fmt.Errorf("请先安装并登录 ChatGPT Desktop")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	// Pin the probe instance to the built-in OpenAI provider: getAuthStatus
	// reports null auth when the active provider has requires_openai_auth=false
	// (e.g. a user model-routing gateway in ~/.codex/config.toml), even though
	// the ChatGPT login token in auth.json is valid. The probe reads account
	// auth only; per-process -c never writes the user's config and turn-side
	// model routing on the Desktop app-server is unaffected.
	cmd := exec.CommandContext(ctx, bin, "-c", "model_provider=openai", "app-server", "--stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	// Reap on every exit path (incidents/2026-10-12 zombie process
	// exhaustion): close stdin, then Kill, then Wait. os/exec requires Wait
	// once StdoutPipe is used; without it each probe leaves a zombie under the
	// runtime and 1s~30s restore retries exhaust kern.maxprocperuid. Wait
	// returning "signal: killed" is the expected outcome and is ignored.
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	init := `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"codex_remote","title":"CordCode","version":"0"},"capabilities":{"experimentalApi":true}}}` + "\n"
	if _, err := stdin.Write([]byte(init)); err != nil {
		return "", "", err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 2<<20)
	for scanner.Scan() {
		line := scanner.Text()
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				AuthMethod string `json:"authMethod"`
				AuthToken  string `json:"authToken"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		if msg.ID == 1 {
			_, _ = stdin.Write([]byte(`{"method":"initialized","params":{}}` + "\n"))
			_, _ = stdin.Write([]byte(`{"id":2,"method":"getAuthStatus","params":{"includeToken":true,"refreshToken":false}}` + "\n"))
			continue
		}
		if msg.ID == 2 {
			if msg.Result.AuthMethod != "chatgpt" || msg.Result.AuthToken == "" {
				return "", "", fmt.Errorf("ChatGPT 未登录")
			}
			accountID, err := accountIDFromJWT(msg.Result.AuthToken)
			if err != nil {
				return "", "", err
			}
			// P1 §3 D4: the real-spawn counter for the probe-economy
			// reconciliation (grep auth_probe). Emitted only on a successful
			// probe that actually spawned the child; the Stat early-exit
			// (not installed) never reaches here, and cache hits live on the
			// caller side (Debug) — this line is caller-independent.
			slog.Info("codex-remote auth_probe")
			return msg.Result.AuthToken, accountID, nil
		}
	}
	return "", "", fmt.Errorf("读取 ChatGPT 登录态超时")
}

// authJWTExpiry returns the token's top-level `exp` claim (ok=false when
// absent or unparseable — the P1 auth cache then serves it until explicitly
// invalidated, the official no-TTL posture; plan §3 D2). `exp` is a
// top-level claim, outside the https://api.openai.com/auth namespace
// accountIDFromJWT reads.
func authJWTExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

func accountIDFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("malformed token")
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return "", err
	}
	var claims struct {
		Auth map[string]any `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	if claims.Auth == nil {
		return "", fmt.Errorf("account id claim missing")
	}
	if id, ok := claims.Auth["chatgpt_account_id"].(string); ok && id != "" {
		return id, nil
	}
	if id, ok := claims.Auth["account_id"].(string); ok && id != "" {
		return id, nil
	}
	return "", fmt.Errorf("account id claim missing")
}
