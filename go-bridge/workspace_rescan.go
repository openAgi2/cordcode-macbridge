package gobridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dshweb "github.com/openAgi2/cordcode-macbridge/agent/dsh-web"
	"github.com/openAgi2/cordcode-macbridge/toolinventory"
)

type workspaceRescanOperation struct {
	ID     string `json:"operationId"`
	Status string `json:"status"`
	done   chan struct{}
	cancel context.CancelFunc
}

type workspaceRescanRequest struct {
	AgentIDs      *[]string `json:"agentIDs"`
	CheckVersions *bool     `json:"checkVersions"`
}

func (s *ManagementServer) handleWorkspaceRescan(w http.ResponseWriter, r *http.Request) {
	if s.cfg.VersionService == nil {
		writeMgmtJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "version_service_unavailable"})
		return
	}
	body, ok := s.readManagementBody(w, r)
	if !ok {
		return
	}
	var request workspaceRescanRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.More() || request.AgentIDs == nil || request.CheckVersions == nil {
		writeMgmtJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	ids := *request.AgentIDs
	if len(ids) == 0 {
		ids = workspaceProductIDs()
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, valid := toolinventory.ProductByID(id); !valid {
			writeMgmtJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_agent_id", "agentId": id})
			return
		}
		if _, duplicate := seen[id]; duplicate {
			writeMgmtJSON(w, http.StatusBadRequest, map[string]string{"error": "duplicate_agent_id", "agentId": id})
			return
		}
		seen[id] = struct{}{}
	}
	sort.Strings(ids)
	if !*request.CheckVersions {
		operationID, err := workspaceOperationID()
		if err != nil {
			writeMgmtJSON(w, http.StatusInternalServerError, map[string]string{"error": "operation_id_unavailable"})
			return
		}
		writeMgmtJSON(w, http.StatusAccepted, workspaceRescanOperation{
			ID: operationID, Status: "inventory_only",
		})
		return
	}

	key := strings.Join(ids, ",")
	s.rescanMu.Lock()
	if operation, active := s.workspaceRescans[key]; active {
		s.rescanMu.Unlock()
		writeMgmtJSON(w, http.StatusAccepted, workspaceRescanOperation{ID: operation.ID, Status: "already_running"})
		return
	}
	parent := s.cfg.WorkspaceContext
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	operationID, err := workspaceOperationID()
	if err != nil {
		cancel()
		s.rescanMu.Unlock()
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]string{"error": "operation_id_unavailable"})
		return
	}
	operation := &workspaceRescanOperation{
		ID: operationID, Status: "started", done: make(chan struct{}), cancel: cancel,
	}
	s.workspaceRescans[key] = operation
	s.rescanMu.Unlock()

	go func() {
		defer close(operation.done)
		defer cancel()
		defer func() {
			s.rescanMu.Lock()
			if s.workspaceRescans[key] == operation {
				delete(s.workspaceRescans, key)
			}
			s.rescanMu.Unlock()
		}()
		for _, id := range ids {
			product, _ := toolinventory.ProductByID(id)
			target, ok := workspaceCheckTarget(product, s.cfg)
			if !ok {
				continue
			}
			_ = s.cfg.VersionService.Check(ctx, target, true)
		}
	}()
	writeMgmtJSON(w, http.StatusAccepted, workspaceRescanOperation{ID: operation.ID, Status: "started"})
}

func workspaceProductIDs() []string {
	products := toolinventory.Products()
	ids := make([]string, 0, len(products))
	for _, product := range products {
		ids = append(ids, product.ID)
	}
	return ids
}

func workspaceOperationID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "workspace-rescan-" + hex.EncodeToString(value[:]), nil
}

func workspaceCheckTarget(product toolinventory.Product, cfg ManagementConfig) (toolinventory.CheckTarget, bool) {
	artifactPath := workspaceArtifactPath(product.ID, cfg)
	if artifactPath == "" {
		return toolinventory.CheckTarget{}, false
	}
	if _, err := os.Stat(artifactPath); err != nil {
		return toolinventory.CheckTarget{}, false
	}
	target := toolinventory.CheckTarget{Product: product, ArtifactPath: artifactPath}
	origin, err := toolinventory.ClassifyInstallOrigin(product, artifactPath, toolinventory.OriginDependencies{})
	if err == nil {
		target.Origin = origin
	}
	recordedNPMPath := cfg.OpenCodeInstallNpmPath
	if product.ID == "dsh-web" && cfg.DataDir != nil {
		if record := dshweb.ReadInstallRecordSnapshot(cfg.DataDir.Path()); record != nil && record.BinPath == artifactPath {
			recordedNPMPath = record.NpmPath
		}
	}
	if origin.Method != toolinventory.InstallMethodNPM && recordedNPMPath != "" {
		if recorded, ok := recordedNPMOrigin(product, artifactPath, origin.RealPath, recordedNPMPath); ok {
			origin = recorded
			target.Origin = origin
		}
	}
	target.Scope = workspaceInstallScope(product, origin, artifactPath, cfg)

	realPath := artifactPath
	if origin.RealPath != "" {
		realPath = origin.RealPath
	} else if resolved, resolveErr := filepath.EvalSymlinks(artifactPath); resolveErr == nil {
		realPath = resolved
	}
	artifactInfo, artifactErr := os.Stat(realPath)
	var manifestPath string
	var manifestInfo os.FileInfo
	if product.NPMPackage != "" && origin.Method == toolinventory.InstallMethodNPM {
		if metadata, metadataErr := toolinventory.ReadNPMPackageMetadata(product, artifactPath); metadataErr == nil {
			target.InstalledVersion = metadata.Version
			manifestPath = metadata.Path
			manifestInfo, _ = os.Stat(metadata.Path)
		}
	}
	if artifactErr == nil {
		target.InstallFingerprint = toolinventory.ArtifactFingerprint(product.ID, realPath, manifestPath, artifactInfo, manifestInfo)
	}
	return target, true
}

func workspaceInstallScope(product toolinventory.Product, origin toolinventory.InstallOrigin, artifactPath string, cfg ManagementConfig) string {
	if origin.Method != toolinventory.InstallMethodNPM {
		return ""
	}
	if product.ID == "dsh-web" && cfg.DataDir != nil {
		if record := dshweb.ReadInstallRecordSnapshot(cfg.DataDir.Path()); record != nil && record.BinPath == artifactPath && record.Scope != "" {
			return record.Scope
		}
	}
	if product.ID == "opencode-web" && cfg.OpenCodeInstallScope != "" {
		return cfg.OpenCodeInstallScope
	}
	if cfg.DataDir != nil {
		for _, name := range []string{"dsh-install", "opencode-install"} {
			if origin.Prefix == filepath.Join(cfg.DataDir.Path(), name) {
				return "cordcode-prefix"
			}
		}
	}
	return "user-global"
}

func recordedNPMOrigin(
	product toolinventory.Product,
	artifactPath, realPath, npmPath string,
) (toolinventory.InstallOrigin, bool) {
	if product.NPMPackage == "" || artifactPath == "" || npmPath == "" {
		return toolinventory.InstallOrigin{}, false
	}
	if info, err := os.Stat(npmPath); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return toolinventory.InstallOrigin{}, false
	}
	resolved := realPath
	if resolved == "" {
		if value, err := filepath.EvalSymlinks(artifactPath); err == nil {
			resolved = value
		} else {
			resolved = artifactPath
		}
	}
	slashReal := filepath.ToSlash(resolved)
	marker := "/node_modules/" + product.NPMPackage + "/"
	index := strings.Index(slashReal, marker)
	if index < 0 {
		return toolinventory.InstallOrigin{}, false
	}
	modules := filepath.FromSlash(slashReal[:index] + "/node_modules")
	prefixParent := filepath.Dir(modules)
	prefix := prefixParent
	if filepath.Base(prefixParent) == "lib" {
		prefix = filepath.Dir(prefixParent)
	}
	return toolinventory.InstallOrigin{
		Method:    toolinventory.InstallMethodNPM,
		PackageID: product.NPMPackage, Prefix: prefix,
		ManagerPath: npmPath, NodeBinDir: filepath.Dir(npmPath),
		RealPath: resolved, EvidencePath: artifactPath,
	}, true
}
