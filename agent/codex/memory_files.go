package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	projectAgentsMemoryFileID = "project:agents"
	globalAgentsMemoryFileID  = "global:agents"
)

func (a *Agent) ListMemoryFiles(_ context.Context) ([]core.MemoryFile, error) {
	candidates := []struct {
		id          string
		scope       string
		description string
		path        string
	}{
		{
			id:          projectAgentsMemoryFileID,
			scope:       "project",
			description: "项目级 Codex 指令文件",
			path:        a.ProjectMemoryFile(),
		},
		{
			id:          globalAgentsMemoryFileID,
			scope:       "global",
			description: "全局 Codex 指令文件",
			path:        a.GlobalMemoryFile(),
		},
	}

	files := make([]core.MemoryFile, 0, len(candidates))
	for _, candidate := range candidates {
		file, ok, err := statCodexMemoryFile(candidate.id, candidate.scope, candidate.description, candidate.path)
		if err != nil {
			return nil, err
		}
		if ok {
			files = append(files, file)
		}
	}
	return files, nil
}

func (a *Agent) ReadMemoryFile(_ context.Context, fileID string) (*core.MemoryFile, error) {
	filePath, scope, description, err := a.resolveMemoryFileDescriptor(fileID)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("memory file not found: %s", fileID)
		}
		return nil, fmt.Errorf("codex: stat memory file %q: %w", filePath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("memory file is a directory: %s", fileID)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("codex: read memory file %q: %w", filePath, err)
	}

	file := buildCodexMemoryFile(fileID, scope, description, filePath, info)
	file.Content = string(content)
	return &file, nil
}

func (a *Agent) resolveMemoryFileDescriptor(fileID string) (path string, scope string, description string, err error) {
	switch fileID {
	case projectAgentsMemoryFileID:
		return a.ProjectMemoryFile(), "project", "项目级 Codex 指令文件", nil
	case globalAgentsMemoryFileID:
		return a.GlobalMemoryFile(), "global", "全局 Codex 指令文件", nil
	default:
		return "", "", "", fmt.Errorf("unknown memory file id: %s", fileID)
	}
}

func statCodexMemoryFile(fileID, scope, description, path string) (core.MemoryFile, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return core.MemoryFile{}, false, nil
		}
		return core.MemoryFile{}, false, fmt.Errorf("codex: stat memory file %q: %w", path, err)
	}
	if info.IsDir() {
		return core.MemoryFile{}, false, nil
	}
	return buildCodexMemoryFile(fileID, scope, description, path, info), true, nil
}

func buildCodexMemoryFile(fileID, scope, description, path string, info os.FileInfo) core.MemoryFile {
	return core.MemoryFile{
		ID:           fileID,
		Name:         filepath.Base(path),
		Path:         path,
		Scope:        scope,
		Description:  description,
		SizeBytes:    info.Size(),
		UpdatedAt:    info.ModTime().UTC(),
		ETag:         codexMemoryFileETag(info),
		ContentType:  "text/markdown",
		Encoding:     "utf-8",
		LastModified: info.ModTime().UTC(),
	}
}

func codexMemoryFileETag(info os.FileInfo) string {
	return fmt.Sprintf("%d-%d", info.Size(), info.ModTime().UTC().UnixNano())
}

var _ core.MemoryFileReader = (*Agent)(nil)
