package transcriptindex

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// sha1Sum is used only to derive a stable index filename from the transcript
// path (matching the exec-plan state-file naming convention).
func sha1Sum(s string) []byte {
	h := sha1.New()
	h.Write([]byte(s))
	return h.Sum(nil)
}

// PersistPath returns the on-disk path for an index under baseDir. Indexes are
// namespaced by backend so Codex and Claude never collide.
func PersistPath(baseDir string, backend Backend, filePath string) string {
	sum := sha1Sum(filePath)
	name := fmt.Sprintf("%s-%x.json", backend, sum[:8])
	return filepath.Join(baseDir, "transcript-index", string(backend), name)
}

// Save persists idx atomically under baseDir (single writer per path; route
// through Store to serialize per session). It uses core.AtomicWriteFile so a
// crash leaves either the previous intact file or the new one, never a tear.
func Save(baseDir string, idx *PageIndex) error {
	if idx == nil {
		return fmt.Errorf("transcriptindex: save nil index")
	}
	path := PersistPath(baseDir, idx.Backend, idx.FilePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return core.AtomicWriteFile(path, data, 0o644)
}

// Load reads and validates a persisted index. It returns (nil, nil) when no
// index exists or when the file is truncated, version-incompatible, extractor
// -incompatible, backend-mismatched, or has an invalid lineage — callers
// rebuild from the transcript in all those cases (design §6.3, §6.5.2). A
// genuine I/O error is returned.
func Load(baseDir string, backend Backend, filePath string) (*PageIndex, error) {
	path := PersistPath(baseDir, backend, filePath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var idx PageIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, nil // corrupt/truncated -> discard
	}
	if idx.Version != IndexVersion ||
		idx.Backend != backend ||
		idx.ExtractorRevision != extractorRevision(backend) {
		return nil, nil
	}
	if !validateLineage(idx.GenerationLineage) {
		return nil, nil
	}
	return &idx, nil
}
