package gobridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/toolinventory"
)

func makeNPMWorkspaceArtifact(t *testing.T) (ManagementConfig, *atomicCounter) {
	t.Helper()
	root := t.TempDir()
	pkg := "opencode-ai"
	artifact := filepath.Join(root, "lib", "node_modules", pkg, "bin", "opencode")
	npm := filepath.Join(root, "bin", "npm")
	for _, path := range []string{artifact, npm} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"name":"opencode-ai","version":"1.19.0"}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(artifact)), "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	calls := &atomicCounter{}
	cfg := ManagementConfig{
		Handlers: NewHandlers(), Token: testMgmtToken, DataDir: NewDataDir(t.TempDir()),
		Agents: map[string]core.Agent{}, OpenCodeInstalledCLI: artifact,
		OpenCodeSource: "managed_local", WorkspaceContext: context.Background(),
		VersionService: toolinventory.NewVersionService(toolinventory.VersionDependencies{
			NPMLatest: func(context.Context, string) (string, error) {
				calls.Add(1)
				return "1.19.1", nil
			},
		}),
	}
	return cfg, calls
}

type atomicCounter struct {
	mu    sync.Mutex
	value int
}

func (c *atomicCounter) Add(delta int) {
	c.mu.Lock()
	c.value += delta
	c.mu.Unlock()
}

func (c *atomicCounter) Load() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func TestWorkspaceRescanUpdatesVersionObservationAndRecipe(t *testing.T) {
	cfg, calls := makeNPMWorkspaceArtifact(t)
	srv := NewManagementServer(cfg)

	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, authJSONRequest(http.MethodPost, "/internal/workspace/rescan", `{"agentIDs":["opencode-web"],"checkVersions":true}`))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("rescan status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var kick struct {
		OperationID string `json:"operationId"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &kick); err != nil {
		t.Fatal(err)
	}
	if kick.Status != "started" || !strings.HasPrefix(kick.OperationID, "workspace-rescan-") {
		t.Fatalf("unexpected kick: %+v", kick)
	}

	deadline := time.Now().Add(2 * time.Second)
	var snapshot WorkspaceSnapshot
	for {
		recorder = httptest.NewRecorder()
		srv.ServeHTTP(recorder, authRequest(http.MethodGet, "/internal/workspace/snapshot"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("snapshot status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		var opencode WorkspaceAgentSnapshot
		for _, agent := range snapshot.Agents {
			if agent.ID == "opencode-web" {
				opencode = agent
			}
		}
		if opencode.VersionCheck.State == "update_available" {
			if opencode.VersionCheck.Target != "1.19.1" || opencode.VersionCheck.UpdateRecipe == nil {
				t.Fatalf("update observation incomplete: %+v", opencode.VersionCheck)
			}
			if !strings.Contains(opencode.VersionCheck.UpdateRecipe.Display, "'opencode-ai@1.19.1'") {
				t.Fatalf("recipe display omitted pinned target: %+v", opencode.VersionCheck.UpdateRecipe)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for version observation: %+v", snapshot.Agents)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls=%d, want 1", calls.Load())
	}
}

func TestWorkspaceRescanRejectsInvalidBodyAndIDs(t *testing.T) {
	cfg, _ := makeNPMWorkspaceArtifact(t)
	srv := NewManagementServer(cfg)
	tests := []struct {
		name string
		body string
		code int
	}{
		{"unknown field", `{"agentIDs":[],"checkVersions":true,"force":true}`, http.StatusBadRequest},
		{"missing ids", `{"checkVersions":true}`, http.StatusBadRequest},
		{"missing check", `{"agentIDs":[]}`, http.StatusBadRequest},
		{"unknown id", `{"agentIDs":["codex-web"],"checkVersions":true}`, http.StatusBadRequest},
		{"duplicate id", `{"agentIDs":["claude","claude"],"checkVersions":true}`, http.StatusBadRequest},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		srv.ServeHTTP(recorder, authJSONRequest(http.MethodPost, "/internal/workspace/rescan", test.body))
		if recorder.Code != test.code {
			t.Fatalf("%s: status=%d body=%s", test.name, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	unauthorized := httptest.NewRequest(http.MethodPost, "/internal/workspace/rescan", strings.NewReader(`{"agentIDs":[],"checkVersions":true}`))
	srv.ServeHTTP(recorder, unauthorized)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", recorder.Code)
	}
}

func TestWorkspaceRescanInventoryOnlyDoesNotCheckVersions(t *testing.T) {
	cfg, calls := makeNPMWorkspaceArtifact(t)
	srv := NewManagementServer(cfg)
	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, authJSONRequest(http.MethodPost, "/internal/workspace/rescan", `{"agentIDs":[],"checkVersions":false}`))
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"inventory_only"`) {
		t.Fatalf("inventory-only response=%d %s", recorder.Code, recorder.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("inventory-only request executed version provider: %d", calls.Load())
	}
}

func TestWorkspaceRescanCoalescesDuplicateInFlightOperation(t *testing.T) {
	cfg, _ := makeNPMWorkspaceArtifact(t)
	release := make(chan struct{})
	cfg.VersionService = toolinventory.NewVersionService(toolinventory.VersionDependencies{
		NPMLatest: func(context.Context, string) (string, error) {
			<-release
			return "1.19.1", nil
		},
	})
	srv := NewManagementServer(cfg)

	first := httptest.NewRecorder()
	srv.ServeHTTP(first, authJSONRequest(http.MethodPost, "/internal/workspace/rescan", `{"agentIDs":["opencode-web"],"checkVersions":true}`))
	second := httptest.NewRecorder()
	srv.ServeHTTP(second, authJSONRequest(http.MethodPost, "/internal/workspace/rescan", `{"agentIDs":["opencode-web"],"checkVersions":true}`))
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses=%d/%d bodies=%s/%s", first.Code, second.Code, first.Body, second.Body)
	}
	var firstKick, secondKick struct {
		OperationID string `json:"operationId"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstKick); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondKick); err != nil {
		t.Fatal(err)
	}
	if firstKick.Status != "started" || secondKick.Status != "already_running" || firstKick.OperationID != secondKick.OperationID {
		t.Fatalf("duplicate operation was not coalesced: first=%+v second=%+v", firstKick, secondKick)
	}
	close(release)
}

func TestWorkspaceCheckTargetUsesExactInstallIdentityAndScope(t *testing.T) {
	cfg, _ := makeNPMWorkspaceArtifact(t)
	product, _ := toolinventory.ProductByID("opencode-web")
	target, ok := workspaceCheckTarget(product, cfg)
	if !ok {
		t.Fatal("target missing")
	}
	if target.InstalledVersion != "1.19.0" || target.Scope != "user-global" || target.InstallFingerprint == "" {
		t.Fatalf("target identity incomplete: %+v", target)
	}
	if target.Origin.Prefix == "" || target.Origin.ManagerPath != filepath.Join(target.Origin.Prefix, "bin", "npm") {
		t.Fatalf("origin manager mismatch: %+v", target.Origin)
	}
}

func TestWorkspaceCheckTargetRejectsMissingArtifact(t *testing.T) {
	cfg := ManagementConfig{OpenCodeInstalledCLI: filepath.Join(t.TempDir(), "missing")}
	product, _ := toolinventory.ProductByID("opencode-web")
	if _, ok := workspaceCheckTarget(product, cfg); ok {
		t.Fatal("missing artifact must not create a check target")
	}
}

func TestWorkspaceCheckTargetUsesRecordedPrefixNPM(t *testing.T) {
	root := t.TempDir()
	pkg := "opencode-ai"
	artifact := filepath.Join(root, "node_modules", pkg, "bin", "opencode")
	manifest := filepath.Join(root, "node_modules", pkg, "package.json")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"opencode-ai","version":"1.19.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	npm := filepath.Join(t.TempDir(), "npm")
	if err := os.WriteFile(npm, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := ManagementConfig{
		OpenCodeInstalledCLI: artifact, OpenCodeInstallNpmPath: npm,
		OpenCodeInstallScope: "cordcode-prefix",
	}
	product, _ := toolinventory.ProductByID("opencode-web")
	target, ok := workspaceCheckTarget(product, cfg)
	if !ok {
		t.Fatal("target missing")
	}
	if target.Origin.Method != toolinventory.InstallMethodNPM ||
		target.Origin.ManagerPath != npm ||
		target.Origin.Prefix != expectedRoot(t, root) ||
		target.Scope != "cordcode-prefix" {
		t.Fatalf("recorded prefix origin mismatch: origin=%+v scope=%q", target.Origin, target.Scope)
	}
}

func expectedRoot(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
