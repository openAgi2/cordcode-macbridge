//go:build realdata

// PR0 canonical-window real capture: drives the REAL production
// handleGetSessionProjectionWindow against owner REAL on-disk kernel checkpoints
// (runtime data dir, session-projection/checkpoints). The turns, revs and slicing
// are all production truth; only the kernel seed replicates RestoreCheckpoint's
// post-validation commit (the store's source-file digest cannot run for
// API-backed codex sources). Not run by default `go test`; invoke with
// -tags realdata and CC_WINDOW_CAPTURE_SPEC="class=checkpointPath,..." plus
// CC_WINDOW_CAPTURE_OUT=/tmp/dir. Never writes into the live data dir — read the
// checkpoint from a copy under /tmp (see evidence README).
package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestRealProjectionWindowCapture(t *testing.T) {
	spec := os.Getenv("CC_WINDOW_CAPTURE_SPEC")
	outDir := os.Getenv("CC_WINDOW_CAPTURE_OUT")
	if spec == "" || outDir == "" {
		t.Skip("CC_WINDOW_CAPTURE_SPEC / CC_WINDOW_CAPTURE_OUT not set")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, entry := range strings.Split(spec, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		if len(parts) != 2 {
			t.Fatalf("bad spec entry %q (want class=path)", entry)
		}
		class, checkpointPath := parts[0], parts[1]

		raw, err := os.ReadFile(checkpointPath)
		if err != nil {
			t.Fatalf("%s: read checkpoint: %v", class, err)
		}
		var checkpoint ProjectionCheckpoint
		if err := json.Unmarshal(raw, &checkpoint); err != nil {
			t.Fatalf("%s: decode checkpoint: %v", class, err)
		}
		if len(checkpoint.Projection.Turns) == 0 || checkpoint.Projection.SyncRev <= 0 {
			t.Fatalf("%s: empty projection in checkpoint", class)
		}

		backendID := checkpoint.BackendID
		sessionID := checkpoint.SessionID

		h := NewHandlers()
		t.Cleanup(func() { h.Shutdown(context.Background()) })
		h.SetDataDir(t.TempDir())
		h.mu.Lock()
		h.agents = map[string]core.Agent{backendID: &fakeAgent{name: backendID}}
		h.mu.Unlock()
		conn := &olderWalkConn{}
		h.eventPublisher.SetConnSyncV2(conn, true)
		h.eventPublisher.SetConnProjectionWindowV1(conn, true)

		// Seed the kernel exactly like RestoreCheckpoint's post-validation commit
		// (projection_kernel.go): reducer restore + ready status + committed cut.
		// coldBaseline=true — the checkpoint was produced by a real cold hydrate.
		k := h.projectionKernel
		k.reducer.Restore(backendID, sessionID, checkpoint.Projection)
		k.mu.Lock()
		session := k.sessionLocked(backendID, sessionID)
		session.status = ProjectionHydrationStatus{Phase: ProjectionHydrateReady}
		session.failureAttempts = 0
		session.lastPersistedRev = checkpoint.ProjectionRev
		session.committedSourceCursor = checkpoint.Source.Cursor
		session.committedSource = ProjectionSourceDescriptor{
			Identity: checkpoint.Source.Identity,
			Cursor:   checkpoint.Source.Cursor,
		}
		session.coldBaseline = true
		k.mu.Unlock()

		writeCapture := func(name string, wireErr *WireError, data map[string]any) {
			payload := map[string]any{"requestId": "capture-" + name}
			if wireErr != nil {
				payload["error"] = wireErr
			} else {
				payload["result"] = data
			}
			encoded, err := json.MarshalIndent(payload, "", " ")
			if err != nil {
				t.Fatalf("%s: marshal %s: %v", class, name, err)
			}
			path := filepath.Join(outDir, class+"-"+name+".json")
			if err := os.WriteFile(path, encoded, 0o644); err != nil {
				t.Fatalf("%s: write %s: %v", class, name, err)
			}
		}

		// dispatch mirrors olderWalkDispatch but carries the checkpoint's OWN
		// backendId（97 个盘上 "codex" checkpoint 是已退役 legacy file-based
		// driver（2026-08-25 退役，见退役影响清单）写的 kernel 遗留；现役家族
		// backendId = "codex-remote"（agent/codex-remote/codexremote.go BackendID），
		// 其 kernel 状态按设计不落盘——handlers_projection.go 的 checkpoint
		// staging 只覆盖 source.Path != "" 的 file-backed/composite 源，codex-remote
		// API-backed 源走 .producer.json 的 R11d producer state。窗口切片本身
		// backend 无关（kernel committed turns 的切片），故 legacy-era kernel 仍是
		// 离线可得的真实数据源；样本谱系在 real-capture/README 声明。）
		dispatch := func(params map[string]any) (*WireError, map[string]any) {
			conn.mu.Lock()
			conn.err, conn.data = nil, nil
			conn.mu.Unlock()
			raw, _ := json.Marshal(params)
			msg := WireMessage{RequestID: "r-capture", BackendID: backendID, Method: "get_session_projection_window", Params: raw}
			h.handleGetSessionProjectionWindow(conn, msg, nil)
			conn.mu.Lock()
			defer conn.mu.Unlock()
			if conn.err != nil {
				return conn.err, nil
			}
			encoded, _ := json.Marshal(conn.data)
			var decoded map[string]any
			_ = json.Unmarshal(encoded, &decoded)
			return nil, decoded
		}

		windowLimit := 20
		if len(checkpoint.Projection.Turns) < windowLimit {
			windowLimit = len(checkpoint.Projection.Turns)
		}

		// 1) window_0: tail-anchored initial window off the REAL committed turns.
		wireErr, first := dispatch(map[string]any{
			"direction": "window_0", "backendId": backendID, "sessionId": sessionID, "limit": windowLimit,
		})
		if wireErr != nil {
			t.Fatalf("%s: window_0 failed: %+v", class, wireErr)
		}
		writeCapture("window0", nil, first)

		// 2) older walk: page back through the real history to the kernel floor.
		steps := 0
		cursor, _ := first["window"].(map[string]any)["nextOlderCursor"].(string)
		for cursor != "" && steps < 8 {
			steps++
			var older map[string]any
			wireErr, older = dispatch(map[string]any{
				"direction": "older", "backendId": backendID, "sessionId": sessionID,
				"cursor": cursor, "limit": windowLimit,
			})
			if wireErr != nil {
				t.Fatalf("%s: older step %d failed: %+v", class, steps, wireErr)
			}
			writeCapture(fmt.Sprintf("older-%d", steps), nil, older)
			window, _ := older["window"].(map[string]any)
			if hasOlder, _ := window["hasOlder"].(bool); !hasOlder {
				break
			}
			cursor, _ = window["nextOlderCursor"].(string)
		}

		// 3) locate: anchor on a REAL mid-history turn id.
		turns := checkpoint.Projection.Turns
		anchor := turns[len(turns)/2].TurnID
		wireErr, located := dispatch(map[string]any{
			"direction": "locate", "backendId": backendID, "sessionId": sessionID,
			"anchorTurnId": anchor, "limit": windowLimit,
		})
		if wireErr != nil {
			t.Fatalf("%s: locate failed: %+v", class, wireErr)
		}
		writeCapture("locate-mid", nil, located)

		// 4) typed error shapes off the real session: scope-mismatch cursor
		// (valid cursor shape — anchor/side/epoch 齐全——但 foreign session scope，
		// 否则 decode 验证先落 cursor_stale（re-freeze：无效 cursor 共享 stale 恢复
		// 契约））与 limit bound。
		foreignErr, _ := dispatch(map[string]any{
			"direction": "older", "backendId": backendID, "sessionId": sessionID,
			"cursor": encodeProjectionWindowCursor(projectionWindowCursor{
				V: 1, BackendID: backendID, SessionID: "foreign-session",
				BridgeEpoch:  h.eventPublisher.BridgeEpoch(),
				AnchorTurnID: anchor, Side: "o",
			}), "limit": windowLimit,
		})
		writeCapture("error-cursor-scope-mismatch", foreignErr, nil)

		limitErr, _ := dispatch(map[string]any{
			"direction": "window_0", "backendId": backendID, "sessionId": sessionID, "limit": maxWindowTurns + 1,
		})
		writeCapture("error-limit-exceeded", limitErr, nil)

		meta := map[string]any{
			"class":          class,
			"backendId":      backendID,
			"sessionId":      sessionID,
			"checkpointPath": checkpointPath,
			"turns":          len(checkpoint.Projection.Turns),
			"syncRev":        checkpoint.Projection.SyncRev,
			"windowLimit":    windowLimit,
			"olderSteps":     steps,
			"locateAnchor":   anchor,
		}
		metaRaw, _ := json.MarshalIndent(meta, "", " ")
		if err := os.WriteFile(filepath.Join(outDir, class+"-meta.json"), metaRaw, 0o644); err != nil {
			t.Fatalf("%s: write meta: %v", class, err)
		}
		fmt.Printf("REAL WINDOW CAPTURE %s: sid=%s turns=%d syncRev=%d olderSteps=%d\n",
			class, sessionID, len(checkpoint.Projection.Turns), checkpoint.Projection.SyncRev, steps)
	}
}
