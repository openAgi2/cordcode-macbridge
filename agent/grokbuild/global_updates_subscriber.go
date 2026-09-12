package grokbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// SubscribeLive exposes a process-wide, replay-free view over Grok's durable
// per-session update journals. Existing files are baselined at their current EOF;
// only later appends are emitted. A file first seen after startup is treated as a
// new live journal and read from zero. This covers both inline Grok (no leader
// socket) and leader mode without spawning or driving Grok.
func (a *Agent) SubscribeLive(ctx context.Context) (<-chan core.Event, error) {
	out := make(chan core.Event, 256)
	go func() {
		defer close(out)
		a.runGlobalUpdatesSubscriber(ctx, out)
	}()
	return out, nil
}

type grokGlobalUpdateFile struct {
	sessionID string
	offset    int64
	codec     *grokUpdateState
}

func (a *Agent) runGlobalUpdatesSubscriber(ctx context.Context, out chan<- core.Event) {
	home := resolveGrokHome(a.grokHome)
	files := make(map[string]*grokGlobalUpdateFile)
	initial := true
	emit := func(events []core.Event) {
		for _, ev := range events {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			default:
				// The live channel is lossy-tolerant, matching Grok's other passive
				// surfaces; the durable journal and cold history remain reconciliation.
			}
		}
	}
	sweep := func() {
		seen := make(map[string]struct{})
		_ = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || entry.Name() != "updates.jsonl" {
				return nil
			}
			seen[path] = struct{}{}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			state := files[path]
			if state == nil {
				state = &grokGlobalUpdateFile{
					sessionID: filepath.Base(filepath.Dir(path)),
					codec:     newGrokUpdateState(),
				}
				// First process-wide sweep baselines every journal that already exists.
				// Journals first observed later are newly created/live.
				if initial {
					state.offset = grokInitialJournalOffset(path, info.Size())
				}
				files[path] = state
			}
			if info.Size() < state.offset {
				// Truncate/rewrite has no replay-safe identity. Re-baseline instead of
				// turning replacement contents into notifications.
				state.offset = info.Size()
				state.codec = newGrokUpdateState()
				return nil
			}
			if info.Size() == state.offset {
				return nil
			}
			tail := &updatesFileTailSubscriber{
				grokHome:    home,
				sessionID:   state.sessionID,
				updateState: state.codec,
			}
			consumed, _, err := tail.drainNew(path, state.offset, func(ev core.Event) {
				if ev.SessionID == "" {
					ev.SessionID = state.sessionID
				}
				emit([]core.Event{ev})
			})
			if err != nil {
				slog.Warn("grokbuild: global updates journal tail failed",
					"session", state.sessionID, "path", path, "error", err)
				return nil
			}
			state.offset += consumed
			return nil
		})
		for path := range files {
			if _, ok := seen[path]; !ok {
				delete(files, path)
			}
		}
		initial = false
	}

	sweep()
	ticker := time.NewTicker(grokUpdatesRelayPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
			if ctx.Err() != nil {
				return
			}
		}
	}
}

func grokInitialJournalOffset(path string, completeSize int64) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return completeSize
	}
	terminalEnd := int64(0)
	active := false
	for start := 0; start < len(data); {
		relativeEnd := bytes.IndexByte(data[start:], '\n')
		if relativeEnd < 0 {
			break
		}
		end := start + relativeEnd + 1
		line := data[start : end-1]
		start = end
		var row struct {
			Method string `json:"method"`
			Params struct {
				Update struct {
					SessionUpdate string `json:"sessionUpdate"`
				} `json:"update"`
			} `json:"params"`
		}
		if json.Unmarshal(line, &row) != nil || !isSessionUpdateMethod(row.Method) {
			continue
		}
		params := extractParams(line)
		if len(params) == 0 || isReplayUpdate(params) {
			continue
		}
		if row.Params.Update.SessionUpdate == "turn_completed" {
			terminalEnd = int64(end)
			active = false
			continue
		}
		active = true
	}
	if active {
		return terminalEnd
	}
	return completeSize
}
