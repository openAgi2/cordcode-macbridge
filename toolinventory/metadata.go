package toolinventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const packageMetadataLimit = 256 * 1024

// PackageMetadata is installation metadata only. It never proves the executable can run.
type PackageMetadata struct {
	Name    string
	Version string
	Path    string
}

// ReadNPMPackageMetadata reads package.json for the exact package owning artifactPath.
// The root name must match the product package exactly; mismatched or malformed data fails.
func ReadNPMPackageMetadata(product Product, artifactPath string) (PackageMetadata, error) {
	if product.NPMPackage == "" || artifactPath == "" {
		return PackageMetadata{}, errors.New("product has no npm package identity")
	}
	realPath, err := filepath.EvalSymlinks(artifactPath)
	if err != nil {
		return PackageMetadata{}, fmt.Errorf("resolve artifact: %w", err)
	}
	slashReal := filepath.ToSlash(realPath)
	marker := "/node_modules/" + product.NPMPackage + "/"
	index := strings.Index(slashReal, marker)
	if index < 0 {
		return PackageMetadata{}, fmt.Errorf("artifact does not belong to %s", product.NPMPackage)
	}
	manifest := filepath.FromSlash(slashReal[:index+len(marker)] + "package.json")
	info, err := os.Stat(manifest)
	if err != nil {
		return PackageMetadata{}, fmt.Errorf("stat package manifest: %w", err)
	}
	if info.Size() > packageMetadataLimit {
		return PackageMetadata{}, errors.New("package manifest exceeds size limit")
	}
	file, err := os.Open(manifest)
	if err != nil {
		return PackageMetadata{}, fmt.Errorf("open package manifest: %w", err)
	}
	defer file.Close()
	var root struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, packageMetadataLimit+1))
	if err := decoder.Decode(&root); err != nil {
		return PackageMetadata{}, fmt.Errorf("decode package manifest: %w", err)
	}
	if decoder.More() {
		return PackageMetadata{}, errors.New("trailing package manifest content")
	}
	if root.Name != product.NPMPackage || strings.TrimSpace(root.Version) == "" {
		return PackageMetadata{}, fmt.Errorf("package manifest identity mismatch: got %q", root.Name)
	}
	return PackageMetadata{Name: root.Name, Version: root.Version, Path: manifest}, nil
}
