package toolinventory

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const grokCheckOutputLimit = 64 * 1024

type grokError struct {
	code   string
	reason string
}

func (e grokError) Error() string { return e.code + ": " + e.reason }

func runGrokUpdateCheck(ctx context.Context, artifactPath string) (GrokUpdateCheck, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, artifactPath, "update", "--check", "--json")
	cmd.Stdin = nil
	cmd.Env = append(cmd.Environ(), "NO_COLOR=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	stdout := &limitedBuffer{max: grokCheckOutputLimit}
	stderr := &limitedBuffer{max: grokCheckOutputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if checkCtx.Err() != nil {
		return GrokUpdateCheck{}, grokError{code: "timeout", reason: "grok update check timed out"}
	}
	if err != nil {
		return GrokUpdateCheck{}, grokError{code: "grok_exit", reason: "grok update check exited unsuccessfully"}
	}
	if stdout.truncated || stderr.truncated {
		return GrokUpdateCheck{}, grokError{code: "output_limit", reason: "grok update check output exceeded the size limit"}
	}
	result, parseErr := parseGrokUpdateCheck(stdout.String())
	if parseErr != nil {
		return GrokUpdateCheck{}, parseErr
	}
	if message := result.ErrorMessage(); message != nil && strings.TrimSpace(*message) != "" {
		return GrokUpdateCheck{}, grokError{code: "grok_error", reason: "grok reported a check error"}
	}
	if result.CurrentVersion == nil || result.LatestVersion == nil || result.Update == nil || result.Installer == nil || result.Channel == nil {
		return GrokUpdateCheck{}, grokError{code: "invalid_grok_check", reason: "grok check JSON is missing required fields"}
	}
	return GrokUpdateCheck{
		CurrentVersion: *result.CurrentVersion,
		LatestVersion:  *result.LatestVersion,
		Update:         *result.Update,
		Installer:      *result.Installer,
		Channel:        *result.Channel,
		AutoUpdate:     result.AutoUpdate != nil && *result.AutoUpdate,
	}, nil
}

type rawGrokUpdateCheck struct {
	CurrentVersion *string `json:"currentVersion"`
	LatestVersion  *string `json:"latestVersion"`
	Update         *bool   `json:"updateAvailable"`
	Installer      *string `json:"installer"`
	Channel        *string `json:"channel"`
	AutoUpdate     *bool   `json:"autoUpdate"`
	Error          *string `json:"error"`
}

// ErrorMessage is deliberately not retained in GrokUpdateCheck: version state
// uses stable codes and non-sensitive reasons, not upstream prose.
func (r rawGrokUpdateCheck) ErrorMessage() *string { return r.Error }

func parseGrokUpdateCheck(raw string) (rawGrokUpdateCheck, error) {
	var result rawGrokUpdateCheck
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return rawGrokUpdateCheck{}, grokError{code: "invalid_grok_json", reason: "grok check output was not valid JSON"}
	}
	for _, field := range []string{
		"currentVersion", "latestVersion", "updateAvailable", "installer", "channel", "error",
	} {
		_, ok := object[field]
		if !ok {
			return rawGrokUpdateCheck{}, grokError{code: "invalid_grok_check", reason: "grok check JSON is missing required fields"}
		}
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return rawGrokUpdateCheck{}, grokError{code: "invalid_grok_json", reason: "grok check JSON contained a wrong value type"}
	}
	return result, nil
}

func grokErrorCode(err error) string {
	var ge grokError
	if errors.As(err, &ge) {
		return ge.code
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	return "network"
}

func grokErrorReason(err error) string {
	var ge grokError
	if errors.As(err, &ge) {
		return ge.reason
	}
	return "grok update check failed"
}
