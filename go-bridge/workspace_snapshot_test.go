package gobridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
)

func TestWorkspaceSnapshotEndpointUsesOwningSources(t *testing.T) {
	oldRegistry := globalDeviceConnRegistry
	globalDeviceConnRegistry = &DeviceConnRegistry{conns: make(map[string][]deviceConnEntry)}
	defer func() { globalDeviceConnRegistry = oldRegistry }()
	globalDeviceConnRegistry.Register("device-b", &fakeRevokableConn{
		metadata: DeviceConnectionMetadata{Kind: DeviceConnectionTransportRelay},
	})
	globalDeviceConnRegistry.Register("device-b", &fakeRevokableConn{
		metadata: DeviceConnectionMetadata{Kind: DeviceConnectionTransportDirect, DirectClass: "private"},
	})

	srv := newTestMgmtServer(map[string]core.Agent{})
	revision := uint64(12)
	srv.cfg.RequestedDrivers = []string{}
	srv.cfg.AgentSelectionRevision = &revision
	srv.cfg.RelayEnabled = true
	srv.cfg.RelayConfigured = true
	srv.cfg.RuntimeIdentity = admission.RuntimeIdentity{PID: 123, BridgeEpoch: 456}

	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, authRequest(http.MethodGet, "/internal/workspace/snapshot"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var snapshot WorkspaceSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != 1 || snapshot.RuntimeIdentity["pid"] != float64(123) {
		t.Fatalf("identity mismatch: %+v", snapshot)
	}
	if snapshot.SelectionRevision == nil || *snapshot.SelectionRevision != 12 {
		t.Fatalf("selection revision mismatch: %+v", snapshot.SelectionRevision)
	}
	if len(snapshot.RequestedDrivers) != 0 || len(snapshot.ActiveDrivers) != 0 {
		t.Fatalf("driver arrays must be empty: %+v", snapshot)
	}
	if len(snapshot.Agents) != 5 {
		t.Fatalf("product agents=%d, want 5", len(snapshot.Agents))
	}
	if len(snapshot.DevicePresence) != 1 || snapshot.DevicePresence[0].DeviceID != "device-b" {
		t.Fatalf("device presence mismatch: %+v", snapshot.DevicePresence)
	}
	if len(snapshot.DevicePresence[0].Transports) != 2 {
		t.Fatalf("transport snapshot mismatch: %+v", snapshot.DevicePresence[0])
	}
	if !snapshot.Relay.Enabled || !snapshot.Relay.Configured || snapshot.Relay.Connected {
		t.Fatalf("relay snapshot mismatch: %+v", snapshot.Relay)
	}
	raw := recorder.Body.String()
	if !json.Valid([]byte(raw)) {
		t.Fatal("snapshot JSON invalid")
	}
	for _, key := range []string{`"deviceId":"device-b"`, `"transports":[`, `"kind":"direct"`, `"kind":"relay"`, `"directClass":"private"`} {
		if !strings.Contains(raw, key) {
			t.Fatalf("wire snapshot missing %s: %s", key, raw)
		}
	}
	for _, forbidden := range []string{"192.168.", "100.64.", "relay:device-b"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("snapshot leaked address %q: %s", forbidden, raw)
		}
	}
}

func TestWorkspaceSnapshotReadsExactOpenCodePackageMetadata(t *testing.T) {
	root := t.TempDir()
	pkg := "opencode-ai"
	artifact := filepath.Join(root, "lib", "node_modules", pkg, "bin", "opencode")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "npm"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"opencode-ai","version":"1.19.0"}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(artifact)), "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ManagementConfig{
		Agents: map[string]core.Agent{}, OpenCodeInstalledCLI: artifact,
		OpenCodeSource: "managed_local", DataDir: NewDataDir(t.TempDir()),
	}
	agents := workspaceAgentSnapshots(cfg)
	var opencode WorkspaceAgentSnapshot
	for _, agent := range agents {
		if agent.ID == "opencode-web" {
			opencode = agent
		}
	}
	if opencode.Installation.State != "installed" || opencode.Installation.InstallMethod != "npm" {
		t.Fatalf("installation mismatch: %+v", opencode.Installation)
	}
	if opencode.Version != "1.19.0" || opencode.VersionSource != "npm_package_json" {
		t.Fatalf("version metadata mismatch: %+v", opencode)
	}
	if opencode.VersionCheck.State != "unchecked" {
		t.Fatalf("package metadata must not claim currency: %+v", opencode.VersionCheck)
	}
}

func TestWorkspaceSnapshotExternalOpenCodeDoesNotRequireLocalCLI(t *testing.T) {
	cfg := ManagementConfig{
		Agents: map[string]core.Agent{}, OpenCodeSource: "external_http", DataDir: NewDataDir(t.TempDir()),
	}
	agents := workspaceAgentSnapshots(cfg)
	var opencode WorkspaceAgentSnapshot
	for _, agent := range agents {
		if agent.ID == "opencode-web" {
			opencode = agent
		}
	}
	if opencode.Installation.State != "not_required" ||
		opencode.Installation.ArtifactKind != "external_service" {
		t.Fatalf("external OpenCode installation mismatch: %+v", opencode.Installation)
	}
}

func TestWorkspaceSnapshotMissingAgentProvidesOfficialGuidance(t *testing.T) {
	t.Setenv("PATH", "")
	cfg := ManagementConfig{
		Agents: map[string]core.Agent{}, DataDir: NewDataDir(t.TempDir()),
	}
	agents := workspaceAgentSnapshots(cfg)
	var grok WorkspaceAgentSnapshot
	for _, agent := range agents {
		if agent.ID == "grokbuild" {
			grok = agent
		}
	}
	if grok.Installation.State != "missing" || grok.InstallGuide == nil {
		t.Fatalf("missing Grok guidance absent: %+v", grok)
	}
	if grok.InstallGuide.Recipe == nil ||
		!strings.Contains(grok.InstallGuide.Recipe.Display, "https://x.ai/cli/install.sh") ||
		grok.InstallGuide.DocumentationURL == "" {
		t.Fatalf("missing Grok guidance incomplete: %+v", grok.InstallGuide)
	}
}
