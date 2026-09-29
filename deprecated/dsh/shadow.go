package dsh

// Shadow node_modules for route 2 (user-global @deepseek-ai/dsh).
//
// The user's npm dsh tree carries the whole runtime family but NOT the SDK
// stdio layer (server/protocol/demo glue/agent-spine) — verified against the
// installed dsh@0.1.0-rc.6 dependency closure. Those four packages are
// vendored in agent/dsh/vendor (rc.6, unmodified, MIT) and glued into a
// shadow tree inside the driver's OWN data dir:
//
//	<workDir>/.cccode-macbridge/dsh/
//	  cordis.yml                          — driver composition (bare names)
//	  node_modules/@deepseek-ai/
//	    dsh-sdk-jsonrpc-{server,protocol,demo}/   — vendored, REAL files
//	    dsh-agent-spine-demo/                     — vendored, REAL files
//	    <every other @deepseek-ai/* in the user's global dsh scope>/  — SYMLINKS
//
// Bare plugin names in cordis.yml resolve beside the config file → the shadow
// node_modules. Symlinked family packages resolve through their realpath back
// into the user's global tree (Node's default symlink semantics), so every
// family transitive dependency runs at the user's installed version — probe
// and reuse, zero installs, zero writes outside our data dir. Refreshed at
// every agent construction so a user dsh upgrade is picked up on restart.

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed vendor
var vendorFS embed.FS

// vendoredPackages are the SDK-layer package names MacBridge ships itself.
// Everything else resolves through symlinks into the user's global tree.
var vendoredPackages = []string{
	"dsh-sdk-jsonrpc-server",
	"dsh-sdk-protocol",
	"dsh-sdk-jsonrpc-demo",
	"dsh-agent-spine-demo",
}

func isVendored(name string) bool {
	for _, v := range vendoredPackages {
		if v == name {
			return true
		}
	}
	return false
}

// ensureShadowTree materializes the shadow node_modules next to the driver's
// cordis.yml and returns the demo bin.js launch entry. Idempotent; refreshed
// on every call so symlinks track the user's current global install.
func ensureShadowTree(dshDataDir string, global *globalDshInstall) (binJS string, err error) {
	scopeDir := filepath.Join(dshDataDir, "node_modules", "@deepseek-ai")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		return "", err
	}

	// 1. Vendored SDK layer: real files from the embedded rc.6 copies.
	for _, name := range vendoredPackages {
		dst := filepath.Join(scopeDir, name)
		if err := syncVendoredPackage(name, dst); err != nil {
			return "", fmt.Errorf("dsh: vendor %s: %w", name, err)
		}
	}

	// 2. Family packages: symlink every @deepseek-ai/* entry of the user's
	// global dsh scope dir (except the vendored names). Broken links are
	// removed so a user uninstall is picked up on the next refresh.
	entries, err := os.ReadDir(global.familyScopeDir)
	if err != nil {
		return "", fmt.Errorf("dsh: read global scope dir: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if isVendored(name) {
			continue
		}
		link := filepath.Join(scopeDir, name)
		target := filepath.Join(global.familyScopeDir, name)
		if err := refreshSymlink(link, target); err != nil {
			return "", fmt.Errorf("dsh: link family package %s: %w", name, err)
		}
	}

	binJS = filepath.Join(scopeDir, "dsh-sdk-jsonrpc-demo", "lib", "bin.js")
	if !fileExists(binJS) {
		return "", fmt.Errorf("dsh: vendored demo bin.js missing at %s", binJS)
	}
	return binJS, nil
}

// syncVendoredPackage writes the embedded package over dst (remove-then-copy
// keeps stale files out; the vendored set is small and fixed).
func syncVendoredPackage(name, dst string) error {
	srcRoot := filepath.Join("vendor", "@deepseek-ai", name)
	if _, err := vendorFS.ReadDir(srcRoot); err != nil {
		return fmt.Errorf("not embedded: %w", err)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return fs.WalkDir(vendorFS, srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := vendorFS.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// refreshSymlink (re)points link at target, tolerating a stale or missing
// link.
func refreshSymlink(link, target string) error {
	if resolved, err := os.Readlink(link); err == nil {
		if resolved == target {
			return nil // up to date
		}
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(target, link)
}

// shadowBinScript is a tiny sanity helper used by tests to prove the vendored
// glue is byte-identical to the embedded copy.
func shadowBinScript(scopeDir string) string {
	return filepath.Join(scopeDir, "dsh-sdk-jsonrpc-demo", "lib", "bin.js")
}
