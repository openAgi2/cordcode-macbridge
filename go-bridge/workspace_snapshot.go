package gobridge

import (
	"errors"
	"os"
	"os/exec"
	"sort"
	"time"

	codexremote "github.com/openAgi2/cordcode-macbridge/agent/codex-remote"
	dshweb "github.com/openAgi2/cordcode-macbridge/agent/dsh-web"
	"github.com/openAgi2/cordcode-macbridge/toolinventory"
)

type WorkspaceAgentInstallation struct {
	State         string `json:"state"`
	ErrorCode     string `json:"errorCode,omitempty"`
	Reason        string `json:"reason,omitempty"`
	ArtifactKind  string `json:"artifactKind,omitempty"`
	InstallMethod string `json:"installMethod,omitempty"`
}

type WorkspaceVersionCheck struct {
	State        string                       `json:"state"`
	CheckedAt    string                       `json:"checkedAt,omitempty"`
	FreshUntil   string                       `json:"freshUntil,omitempty"`
	Source       string                       `json:"source,omitempty"`
	Channel      string                       `json:"channel,omitempty"`
	Installed    string                       `json:"installedVersion,omitempty"`
	Target       string                       `json:"targetVersion,omitempty"`
	Code         string                       `json:"code,omitempty"`
	Reason       string                       `json:"reason,omitempty"`
	UpdateRecipe *toolinventory.CommandRecipe `json:"updateRecipe,omitempty"`
}

type WorkspaceAgentSnapshot struct {
	ID            string                         `json:"id"`
	Installation  WorkspaceAgentInstallation     `json:"installation"`
	InstallGuide  *toolinventory.InstallGuidance `json:"installGuidance,omitempty"`
	Version       string                         `json:"version,omitempty"`
	VersionBuild  string                         `json:"versionBuild,omitempty"`
	VersionSource string                         `json:"versionSource,omitempty"`
	VersionCheck  WorkspaceVersionCheck          `json:"versionCheck"`
	Readiness     *AgentProviderDescriptor       `json:"readiness,omitempty"`
}

type WorkspaceRelaySnapshot struct {
	Enabled    bool `json:"enabled"`
	Configured bool `json:"configured"`
	Connected  bool `json:"connected"`
}

type WorkspaceSnapshot struct {
	SchemaVersion     int                      `json:"schemaVersion"`
	RuntimeIdentity   map[string]interface{}   `json:"runtimeIdentity"`
	SelectionRevision *uint64                  `json:"selectionRevision"`
	RequestedDrivers  []string                 `json:"requestedDrivers"`
	ActiveDrivers     []string                 `json:"activeDrivers"`
	SampledAt         string                   `json:"sampledAt"`
	Agents            []WorkspaceAgentSnapshot `json:"agents"`
	DevicePresence    []DevicePresence         `json:"devicePresence"`
	Relay             WorkspaceRelaySnapshot   `json:"relay"`
}

func buildWorkspaceSnapshot(cfg ManagementConfig, identity map[string]interface{}, relayConnected bool) WorkspaceSnapshot {
	active := make([]string, 0, len(cfg.Agents))
	for id := range cfg.Agents {
		active = append(active, id)
	}
	sort.Strings(active)
	requested := append([]string(nil), cfg.RequestedDrivers...)
	if requested == nil {
		requested = []string{}
	}
	sort.Strings(requested)
	return WorkspaceSnapshot{
		SchemaVersion:     1,
		RuntimeIdentity:   identity,
		SelectionRevision: cfg.AgentSelectionRevision,
		RequestedDrivers:  requested,
		ActiveDrivers:     active,
		SampledAt:         time.Now().UTC().Format(time.RFC3339Nano),
		Agents:            workspaceAgentSnapshots(cfg),
		DevicePresence:    globalDeviceConnRegistry.PresenceSnapshot(),
		Relay: WorkspaceRelaySnapshot{
			Enabled:    cfg.RelayEnabled,
			Configured: cfg.RelayConfigured,
			Connected:  cfg.RelayEnabled && cfg.RelayConfigured && relayConnected,
		},
	}
}

func workspaceAgentSnapshots(cfg ManagementConfig) []WorkspaceAgentSnapshot {
	out := make([]WorkspaceAgentSnapshot, 0, 8)
	readiness := make(map[string]AgentProviderDescriptor)
	for _, descriptor := range BuildAllAgentDescriptors(cfg.Agents, cfg.CodexBackendMode, cfg.DetectionCfg) {
		readiness[descriptor.ID] = descriptor
	}
	for _, product := range toolinventory.Products() {
		artifactPath := workspaceArtifactPath(product.ID, cfg)
		installation := WorkspaceAgentInstallation{State: "installed", ArtifactKind: "cli"}
		versionCheck := WorkspaceVersionCheck{State: "unchecked"}
		if product.ID == "codex-remote" && artifactPath != "" {
			installation.ArtifactKind = "app_bundle_cli"
		}
		if artifactPath == "" {
			if product.ID == "opencode-web" && cfg.OpenCodeSource == "external_http" {
				installation.State = "not_required"
				installation.ArtifactKind = "external_service"
			} else {
				installation.State = "missing"
			}
		} else if _, err := os.Stat(artifactPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				installation.State = "missing"
				installation.ErrorCode = "artifact_missing"
				installation.Reason = "configured artifact no longer exists"
			} else {
				installation.State = "failed"
				installation.ErrorCode = "artifact_stat_failed"
				installation.Reason = "configured artifact could not be inspected"
			}
		}

		snapshot := WorkspaceAgentSnapshot{
			ID: product.ID, Installation: installation, VersionCheck: versionCheck,
		}
		if descriptor, active := readiness[product.ID]; active {
			value := descriptor
			snapshot.Readiness = &value
		}
		if installation.State == "missing" {
			npmPath, _ := exec.LookPath("npm")
			guidance := toolinventory.BuildInstallGuidance(product, npmPath)
			snapshot.InstallGuide = &guidance
		}
		if product.ID == "codex-remote" && artifactPath != "" && cfg.CodexDesktopShortVersion != "" {
			snapshot.Version = cfg.CodexDesktopShortVersion
			snapshot.VersionBuild = cfg.CodexDesktopBuildVersion
			snapshot.VersionSource = "chatgpt_desktop_plist"
		}
		if artifactPath != "" && installation.State == "installed" && product.NPMPackage != "" {
			origin, err := toolinventory.ClassifyInstallOrigin(product, artifactPath, toolinventory.OriginDependencies{})
			if err != nil {
				snapshot.VersionCheck = WorkspaceVersionCheck{
					State: "failed", Code: "origin_classify_failed", Reason: "installation origin could not be identified",
				}
			} else {
				installation.InstallMethod = string(origin.Method)
				if installation.InstallMethod == "" {
					installation.InstallMethod = "unknown"
				}
				snapshot.Installation = installation
				if origin.Method == toolinventory.InstallMethodNPM {
					metadata, metadataErr := toolinventory.ReadNPMPackageMetadata(product, artifactPath)
					if metadataErr != nil {
						snapshot.VersionCheck = WorkspaceVersionCheck{
							State: "failed", Code: "package_metadata_failed", Reason: "package metadata could not be read",
						}
					} else {
						snapshot.Version = metadata.Version
						snapshot.VersionSource = "npm_package_json"
						// Package metadata alone does not prove currency; latest checking remains S5.
						snapshot.VersionCheck = WorkspaceVersionCheck{State: "unchecked"}
					}
				}
			}
		}
		if cfg.VersionService != nil {
			if observation, ok := cfg.VersionService.Snapshot(product.ID); ok {
				snapshot.VersionCheck = workspaceVersionCheck(observation)
				if observation.InstalledVersion != "" {
					snapshot.Version = observation.InstalledVersion
				}
			}
		}
		out = append(out, snapshot)
	}
	return out
}

func workspaceVersionCheck(observation toolinventory.VersionObservation) WorkspaceVersionCheck {
	check := WorkspaceVersionCheck{
		State: string(observation.State), Source: string(observation.Source),
		Channel: observation.Channel, Installed: observation.InstalledVersion,
		Target: observation.TargetVersion, Code: observation.ErrorCode, Reason: observation.Reason,
		UpdateRecipe: observation.UpdateRecipe,
	}
	if !observation.CheckedAt.IsZero() {
		check.CheckedAt = observation.CheckedAt.UTC().Format(time.RFC3339Nano)
	}
	if !observation.FreshUntil.IsZero() {
		check.FreshUntil = observation.FreshUntil.UTC().Format(time.RFC3339Nano)
	}
	return check
}

func workspaceArtifactPath(productID string, cfg ManagementConfig) string {
	switch productID {
	case "claude", "grokbuild":
		if path, err := exec.LookPath(productNameForRuntimeLookup(productID)); err == nil {
			return path
		}
		return ""
	case "codex-remote":
		return codexremote.ResolveChatGPTCodexInstallPath()
	case "dsh-web":
		if cfg.DataDir != nil {
			return dshweb.FindDSHBinary(cfg.DataDir.Path())
		}
		return ""
	case "opencode-web":
		return cfg.OpenCodeInstalledCLI
	default:
		return ""
	}
}

func productNameForRuntimeLookup(productID string) string {
	if productID == "grokbuild" {
		return "grok"
	}
	return productID
}
