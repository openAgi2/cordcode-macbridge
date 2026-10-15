package toolinventory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstallMethod is a positively identified installation manager. Unknown is a valid
// observation and must be surfaced as an unsupported update source, never as npm/brew.
type InstallMethod string

const (
	InstallMethodUnknown InstallMethod = "unknown"
	InstallMethodNPM     InstallMethod = "npm"
)

// InstallOrigin identifies the manager that owns the exact artifact. It intentionally
// does not infer from a symlink directory such as /opt/homebrew/bin.
type InstallOrigin struct {
	Method       InstallMethod
	PackageID    string
	Prefix       string
	ManagerPath  string
	NodeBinDir   string
	RealPath     string
	EvidencePath string
}

// OriginDependencies keeps filesystem access injectable and prevents ambient PATH fallback.
type OriginDependencies struct {
	RealPath     func(string) (string, error)
	IsExecutable func(string) bool
}

func defaultOriginDependencies() OriginDependencies {
	return OriginDependencies{
		RealPath: func(path string) (string, error) { return filepath.EvalSymlinks(path) },
		IsExecutable: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
		},
	}
}

// ClassifyInstallOrigin classifies the exact artifact path. A missing artifact is an
// installation-locating error; an identifiable but unsupported source is Unknown, not error.
func ClassifyInstallOrigin(product Product, artifactPath string, deps OriginDependencies) (InstallOrigin, error) {
	if artifactPath == "" {
		return InstallOrigin{}, errors.New("empty artifact path")
	}
	if deps.RealPath == nil || deps.IsExecutable == nil {
		deps = defaultOriginDependencies()
	}
	realPath, err := deps.RealPath(artifactPath)
	if err != nil {
		return InstallOrigin{}, fmt.Errorf("resolve artifact: %w", err)
	}
	if !deps.IsExecutable(realPath) {
		return InstallOrigin{}, fmt.Errorf("artifact is not a regular file: %s", realPath)
	}
	origin := InstallOrigin{Method: InstallMethodUnknown, RealPath: realPath, EvidencePath: artifactPath}
	if product.NPMPackage == "" {
		return origin, nil
	}

	slashReal := filepath.ToSlash(realPath)
	marker := "/node_modules/" + product.NPMPackage + "/"
	index := strings.Index(slashReal, marker)
	if index < 0 {
		return origin, nil
	}
	modules := filepath.FromSlash(slashReal[:index] + "/node_modules")
	prefixParent := filepath.Dir(modules)
	if filepath.Base(prefixParent) != "lib" {
		return origin, nil
	}
	prefix := filepath.Dir(prefixParent)
	manager := filepath.Join(prefix, "bin", "npm")
	if !deps.IsExecutable(manager) {
		// Do not substitute another npm from PATH: it may install into a different prefix.
		return origin, nil
	}
	origin.Method = InstallMethodNPM
	origin.PackageID = product.NPMPackage
	origin.Prefix = prefix
	origin.ManagerPath = manager
	origin.NodeBinDir = filepath.Dir(manager)
	return origin, nil
}
