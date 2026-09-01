package codexremote

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func defaultChatGPTCodexPath() string {
	return "/Applications/ChatGPT.app/Contents/Resources/codex"
}

func loadChatGPTAuth(ctx context.Context) (token, accountID string, err error) {
	bin := defaultChatGPTCodexPath()
	if _, statErr := os.Stat(bin); statErr != nil {
		return "", "", fmt.Errorf("请先安装并登录 ChatGPT Desktop")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "app-server", "--stdio")
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
	defer func() { _ = cmd.Process.Kill() }()
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
			return msg.Result.AuthToken, accountID, nil
		}
	}
	return "", "", fmt.Errorf("读取 ChatGPT 登录态超时")
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
