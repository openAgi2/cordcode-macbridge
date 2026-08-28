package codexremote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const oauthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
const enrollScope = "codex.remote_control.enroll"

func randomB64URL(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (p *PairingController) startStepUp(accountID string) (authorizeURL string, wait func(context.Context) (string, error), err error) {
	state := randomB64URL(32)
	verifier := randomB64URL(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	var ln net.Listener
	var listenPort int
	for _, port := range []int{1455, 1457} {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			listenPort = port
			break
		}
	}
	if ln == nil {
		return "", nil, fmt.Errorf("step-up callback ports busy")
	}
	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", listenPort)
	codeCh := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/callback" || r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid callback", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Authorization failed", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "已授权。请回到 CordCode Link 输入电脑配对码。")
		select {
		case codeCh <- code:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", oauthClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", enrollScope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("originator", "Codex Desktop")
	q.Set("reauth", "remote_control")
	q.Set("max_age", "0")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("allowed_workspace_id", accountID)
	q.Set("current_workspace_id", accountID)
	authorizeURL = "https://auth.openai.com/oauth/authorize?" + q.Encode()
	wait = func(ctx context.Context) (string, error) {
		defer func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx2)
		}()
		select {
		case code := <-codeCh:
			return p.exchangeStepUp(ctx, code, redirectURI, verifier)
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return authorizeURL, wait, nil
}

func (p *PairingController) exchangeStepUp(ctx context.Context, code, redirectURI, verifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", oauthClientID)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://auth.openai.com/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Warn("codex-remote pairing step-up exchange failed", "status", resp.StatusCode)
		return "", fmt.Errorf("step-up exchange HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.AccessToken == "" {
		return "", fmt.Errorf("step-up token missing")
	}
	return parsed.AccessToken, nil
}

func openBrowser(rawURL string) error {
	return exec.Command("/usr/bin/open", rawURL).Start()
}
