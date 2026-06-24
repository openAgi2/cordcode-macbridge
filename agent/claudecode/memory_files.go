package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	projectClaudeMemoryFileID = "project:claude"
	globalClaudeMemoryFileID  = "global:claude"
)

func (a *Agent) ListMemoryFiles(_ context.Context) ([]core.MemoryFile, error) {
	candidates := []struct {
		id          string
		scope       string
		description string
		path        string
	}{
		{
			id:          projectClaudeMemoryFileID,
			scope:       "project",
			description: "项目级 Claude 指令文件",
			path:        a.ProjectMemoryFile(),
		},
		{
			id:          globalClaudeMemoryFileID,
			scope:       "global",
			description: "全局 Claude 指令文件",
			path:        a.GlobalMemoryFile(),
		},
	}

	files := make([]core.MemoryFile, 0, len(candidates))
	for _, candidate := range candidates {
		file, ok, err := statMemoryFile(candidate.id, candidate.scope, candidate.description, candidate.path)
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
		return nil, fmt.Errorf("claudecode: stat memory file %q: %w", filePath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("memory file is a directory: %s", fileID)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("claudecode: read memory file %q: %w", filePath, err)
	}

	file := buildMemoryFile(fileID, scope, description, filePath, info)
	file.Content = string(content)
	return &file, nil
}

func (a *Agent) resolveMemoryFileDescriptor(fileID string) (path string, scope string, description string, err error) {
	switch fileID {
	case projectClaudeMemoryFileID:
		return a.ProjectMemoryFile(), "project", "项目级 Claude 指令文件", nil
	case globalClaudeMemoryFileID:
		return a.GlobalMemoryFile(), "global", "全局 Claude 指令文件", nil
	default:
		return "", "", "", fmt.Errorf("unknown memory file id: %s", fileID)
	}
}

func statMemoryFile(fileID, scope, description, path string) (core.MemoryFile, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return core.MemoryFile{}, false, nil
		}
		return core.MemoryFile{}, false, fmt.Errorf("claudecode: stat memory file %q: %w", path, err)
	}
	if info.IsDir() {
		return core.MemoryFile{}, false, nil
	}
	return buildMemoryFile(fileID, scope, description, path, info), true, nil
}

func buildMemoryFile(fileID, scope, description, path string, info os.FileInfo) core.MemoryFile {
	return core.MemoryFile{
		ID:           fileID,
		Name:         filepath.Base(path),
		Path:         path,
		Scope:        scope,
		Description:  description,
		SizeBytes:    info.Size(),
		UpdatedAt:    info.ModTime().UTC(),
		ETag:         memoryFileETag(info),
		Content:      "",
		ContentType:  "text/markdown",
		Encoding:     "utf-8",
		LastModified: info.ModTime().UTC(),
	}
}

func memoryFileETag(info os.FileInfo) string {
	mod := info.ModTime().UTC().UnixNano()
	return fmt.Sprintf("%d-%d", info.Size(), mod)
}

var _ core.MemoryFileReader = (*Agent)(nil)
