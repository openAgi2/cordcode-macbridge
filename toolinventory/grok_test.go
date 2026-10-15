package toolinventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const grokSampleJSON = `{
  "currentVersion":"1.0.52",
  "latestVersion":"1.0.52",
  "updateAvailable":false,
  "installer":"internal",
  "channel":"stable",
  "autoUpdate":true,
  "error":null
}`

func TestParseGrokUpdateCheckAcceptsArchivedShape(t *testing.T) {
	result, err := parseGrokUpdateCheck(grokSampleJSON)
	if err != nil {
		t.Fatal(err)
	}
	if *result.CurrentVersion != "1.0.52" || *result.LatestVersion != "1.0.52" || *result.Update ||
		*result.Installer != "internal" || *result.Channel != "stable" || *result.AutoUpdate != true || result.Error != nil {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestParseGrokUpdateCheckRejectsWrongRootOrMissingFields(t *testing.T) {
	for name, raw := range map[string]string{
		"array":                   `[]`,
		"trailing":                grokSampleJSON + ` {}`,
		"case-insensitive fields": `{"CurrentVersion":"1.0.0","LatestVersion":"1.0.1","UpdateAvailable":true,"Installer":"internal","Channel":"stable","Error":null}`,
	} {
		if _, err := parseGrokUpdateCheck(raw); err == nil {
			t.Fatalf("%s: expected parse failure", name)
		}
	}
}

func TestRunGrokUpdateCheckRejectsReportedErrorAndBadExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grok")
	errorJSON := strings.Replace(grokSampleJSON, `"error":null`, `"error":"network unavailable"`, 1)
	script := "#!/bin/sh\nprintf '%s\\n' '" + strings.ReplaceAll(errorJSON, "'", `'\''`) + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runGrokUpdateCheck(context.Background(), path); err == nil || grokErrorCode(err) != "grok_error" {
		t.Fatalf("expected reported error, got %v", err)
	}

	failing := filepath.Join(t.TempDir(), "grok-fail")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runGrokUpdateCheck(context.Background(), failing); err == nil || grokErrorCode(err) != "grok_exit" {
		t.Fatalf("expected exit error, got %v", err)
	}

	missing := filepath.Join(t.TempDir(), "grok-missing")
	if err := os.WriteFile(missing, []byte("#!/bin/sh\nprintf '%s\\n' '{\"currentVersion\":\"1.0.0\",\"latestVersion\":\"1.0.1\",\"updateAvailable\":true}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runGrokUpdateCheck(context.Background(), missing); err == nil || grokErrorCode(err) != "invalid_grok_check" {
		t.Fatalf("expected missing-field failure, got %v", err)
	}
}

func TestGrokRunnerTimesOutAndReapsProcessGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grok-hang")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := runGrokUpdateCheck(ctx, path); err == nil || grokErrorCode(err) != "timeout" {
		t.Fatalf("expected timeout, got %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("process group was not reaped promptly: %s", time.Since(started))
	}
}
