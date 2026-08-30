package gobridge

// Projection Window producer core (PERF-S4B).
//
// Canonical freeze: docs/protocol/bridge-v1.md §Projection Window (FROZEN SPEC, R1–R10)
// plus the window-anchoring paragraph (re-freeze 65000ac: window_0/latest tail-anchored,
// truncation sides, head/tail nullability, resume presence, malformed cursor → cursor_stale).
//
// Layering (S4B brief): the ProjectionReducer/Kernel remain the single truth and single
// writer. This file only SLICES committed kernel snapshots into turn-aligned bounded
// windows for delivery. It never reduces, mutates, or drops events: live patches keep
// riding the existing projection_patch pipe behind the same snapshot fence (R4 — there is
// no second pipe). Cursors are bridge-owned, opaque, and scope-bound (R1/R2); the client
// never sees upstream producer pagination artifacts.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
)

// R5 assertable bounds. maxWindowTurns caps the requested limit; maxWindowEncodedBytes
// caps the encoded window payload accumulation (a single turn larger than the budget is
// still served alone — the bound limits accumulation and never splits a turn).
const (
	maxWindowTurns         = 256
	maxWindowEncodedBytes  = 4 << 20
	defaultWindowTurns     = 100
	windowEncodingOverhead = 4 << 10
)

const (
	projectionWindowDirectionWindow0 = "window_0"
	projectionWindowDirectionOlder   = "older"
	projectionWindowDirectionNewer   = "newer"
	projectionWindowDirectionLatest  = "latest"
	projectionWindowDirectionLocate  = "locate"
)

// Typed window errors. The handler maps these onto the frozen WireError codes; nothing
// outside the frozen set is invented here (R9/R10).
var (
	errProjectionWindowScopeMismatch = errors.New("projection window cursor scope mismatch")
	errProjectionWindowCursorStale   = errors.New("projection window cursor stale")
	errProjectionWindowLocateOut     = errors.New("projection window locate anchor out of window")
)

// GetSessionProjectionWindowParams — get_session_projection_window request params.
// Mirrors docs/protocol/schema/bridge-v1.types.ts BridgeGetSessionProjectionWindowParams.
type GetSessionProjectionWindowParams struct {
	SessionID    string `json:"sessionId"`
	Directory    string `json:"directory,omitempty"`
	BackendID    string `json:"backendId"`
	Direction    string `json:"direction"`
	Cursor       string `json:"cursor,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	AnchorTurnID string `json:"anchorTurnId,omitempty"`
}

// ProjectionWindowDescriptor mirrors BridgeProjectionWindow. Head/TailTurnID are nil only
// for an empty projection; a non-empty window always carries both (anchoring paragraph).
type ProjectionWindowDescriptor struct {
	WindowID        string  `json:"windowId"`
	Generation      uint64  `json:"generation"`
	Coverage        string  `json:"coverage"` // "full" | "window"
	HeadTurnID      *string `json:"headTurnId"`
	TailTurnID      *string `json:"tailTurnId"`
	HasOlder        bool    `json:"hasOlder"`
	HasNewer        bool    `json:"hasNewer"`
	NextOlderCursor string  `json:"nextOlderCursor,omitempty"`
	NextNewerCursor string  `json:"nextNewerCursor,omitempty"`
}

// ProjectionWindowResponse mirrors BridgeGetSessionProjectionWindowResult.
type ProjectionWindowResponse struct {
	Window  ProjectionWindowDescriptor `json:"window"`
	Turns   []TurnProjection           `json:"turns"`
	SyncRev int                        `json:"syncRev"`
	Resume  *ProjectionWindowResume    `json:"resume,omitempty"`
}

type ProjectionWindowResume struct {
	Kind string `json:"kind"`
}

// projectionWindowCursor is the wire cursor payload. It is bridge-owned and opaque to the
// client (base64url JSON, version-tagged). Scope = (backendId, bridgeEpoch, sessionId);
// AnchorTurnID + Side define the boundary the next page must be adjacent to. No upstream
// producer cursor ever enters this struct (R2).
type projectionWindowCursor struct {
	V            int    `json:"v"`
	BridgeEpoch  string `json:"be"`
	BackendID    string `json:"b"`
	SessionID    string `json:"s"`
	AnchorTurnID string `json:"a"`
	Side         string `json:"d"` // "o" older boundary | "n" newer boundary
}

func encodeProjectionWindowCursor(cursor projectionWindowCursor) string {
	payload, err := json.Marshal(cursor)
	if err != nil {
		// Struct fields are strings/ints; marshal cannot fail in practice. An empty
		// cursor string is invalid on decode and maps to cursor_stale downstream.
		return ""
	}
	return base64.URLEncoding.EncodeToString(payload)
}

func decodeProjectionWindowCursor(raw string) (projectionWindowCursor, error) {
	payload, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return projectionWindowCursor{}, errProjectionWindowCursorStale
	}
	var cursor projectionWindowCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.V != 1 || cursor.AnchorTurnID == "" ||
		(cursor.Side != "o" && cursor.Side != "n") {
		// Re-freeze: an undecodable cursor is not a distinct error — it shares the
		// cursor_stale recovery contract (discard chain, re-issue window_0).
		return projectionWindowCursor{}, errProjectionWindowCursorStale
	}
	return cursor, nil
}

// projectionWindowGeneration is a process-global monotonic counter. A global counter is
// monotonic for every (backendId, sessionId) pair and resets with the process (bridge
// epoch), satisfying R6's "monotonic per (backendId, sessionId) within one bridgeEpoch"
// with zero per-session state to bound or clean up on disconnect.
var projectionWindowGeneration atomic.Uint64

// validateProjectionWindowCursorScope enforces R1 before any kernel access: backend and
// session must match the request, and the minting epoch must still be current (R6).
func validateProjectionWindowCursorScope(cursor projectionWindowCursor, backendID, sessionID, bridgeEpoch string) error {
	if cursor.BackendID != backendID || cursor.SessionID != sessionID {
		return errProjectionWindowScopeMismatch
	}
	if cursor.BridgeEpoch != bridgeEpoch {
		return errProjectionWindowCursorStale
	}
	return nil
}

// turnEncodedSizes marshals each turn once so window slicing can enforce the byte budget
// without repeated whole-payload marshals.
func turnEncodedSizes(turns []TurnProjection) ([]int, error) {
	sizes := make([]int, len(turns))
	for index, turn := range turns {
		payload, err := json.Marshal(turn)
		if err != nil {
			return nil, fmt.Errorf("window turn encode: %w", err)
		}
		sizes[index] = len(payload)
	}
	return sizes, nil
}

type projectionWindowSlice struct {
	turns []TurnProjection
	start int // index of first included turn in proj.Turns
	end   int // index AFTER last included turn in proj.Turns
}

// growWindowRange grows a contiguous [start,end) range from the boundary side until the
// turn-count or byte budget binds. The boundary-adjacent turn is ALWAYS included — the
// byte bound limits accumulation and never yields an empty page or splits a turn (R5,
// anchoring paragraph). Truncation therefore always happens on the side farthest from
// the boundary.
func growWindowRange(sizes []int, limit int, boundaryIndex int, endAnchored bool) (int, int) {
	n := len(sizes)
	count := 0
	bytes := 0
	budget := maxWindowEncodedBytes - windowEncodingOverhead
	if endAnchored {
		start := boundaryIndex
		if start-1 >= 0 {
			// Boundary-adjacent turn is unconditionally included.
			bytes += sizes[start-1]
			count++
			start = start - 1
		}
		for index := start - 1; index >= 0; index-- {
			if count >= limit || bytes+sizes[index] > budget {
				break
			}
			bytes += sizes[index]
			count++
			start = index
		}
		return start, boundaryIndex
	}
	end := boundaryIndex
	if boundaryIndex < n {
		// Boundary-adjacent turn is unconditionally included.
		bytes += sizes[boundaryIndex]
		count++
		end = boundaryIndex + 1
	}
	for index := end; index < n; index++ {
		if count >= limit || bytes+sizes[index] > budget {
			break
		}
		bytes += sizes[index]
		count++
		end = index + 1
	}
	return boundaryIndex, end
}

// sliceProjectionWindow is the pure admission core: it cuts a turn-aligned bounded window
// out of one committed kernel snapshot. No I/O, no state, no mutation — the snapshot fence
// (R4) and the live patch pipe are owned by the caller. The returned descriptor expresses
// the remainder via hasOlder/hasNewer + next*Cursor (cursor present iff flag, R5).
func sliceProjectionWindow(
	backendID, sessionID, bridgeEpoch string,
	proj SessionProjection,
	params GetSessionProjectionWindowParams,
) (ProjectionWindowResponse, error) {
	return sliceProjectionWindowWithUpstream(backendID, sessionID, bridgeEpoch, proj, params, false)
}

// sliceProjectionWindowWithUpstream is the R11d-honest slice: hasOlderUpstream is
// the backend-private producer fact ("more upstream history exists but is not yet
// hydrated"). When the kernel slice reaches its own front but upstream is not at
// EOF, hasOlder MUST be true (never report "session start" for "not loaded").
func sliceProjectionWindowWithUpstream(
	backendID, sessionID, bridgeEpoch string,
	proj SessionProjection,
	params GetSessionProjectionWindowParams,
	hasOlderUpstream bool,
) (ProjectionWindowResponse, error) {
	turns := proj.Turns
	limit := params.Limit
	if limit <= 0 {
		limit = defaultWindowTurns
	}
	sizes, err := turnEncodedSizes(turns)
	if err != nil {
		return ProjectionWindowResponse{}, err
	}

	var slice projectionWindowSlice
	hasOlder := false
	hasNewer := false
	nextOlderAnchor := ""
	nextNewerAnchor := ""
	empty := len(turns) == 0
	// Set only by the older direction when its page is empty (anchor == kernel
	// front): the resume anchor for the newer-side cursor of that empty page.
	pageEmptyNewerAnchor := ""

	switch params.Direction {
	case projectionWindowDirectionWindow0, projectionWindowDirectionLatest:
		slice.start, slice.end = growWindowRange(sizes, limit, len(turns), true)
		hasOlder = slice.start > 0
		// Tail-anchored: the committed live tail is inside the window.
		hasNewer = false
	case projectionWindowDirectionOlder:
		cursor, err := decodeProjectionWindowCursor(params.Cursor)
		if err != nil {
			return ProjectionWindowResponse{}, err
		}
		if err := validateProjectionWindowCursorScope(cursor, backendID, sessionID, bridgeEpoch); err != nil {
			return ProjectionWindowResponse{}, err
		}
		boundary := indexOfTurn(turns, cursor.AnchorTurnID)
		if boundary < 0 {
			// Anchor turn left kernel retention (R6 retention miss).
			return ProjectionWindowResponse{}, errProjectionWindowCursorStale
		}
		slice.start, slice.end = growWindowRange(sizes, limit, boundary, true)
		// The boundary turn itself follows this page in the chain.
		hasOlder = slice.start > 0
		hasNewer = true
		// Anchor == kernel front yields an honest EMPTY page (nothing committed
		// below); the newer chain resumes at the anchor itself.
		pageEmptyNewerAnchor = cursor.AnchorTurnID
	case projectionWindowDirectionNewer:
		cursor, err := decodeProjectionWindowCursor(params.Cursor)
		if err != nil {
			return ProjectionWindowResponse{}, err
		}
		if err := validateProjectionWindowCursorScope(cursor, backendID, sessionID, bridgeEpoch); err != nil {
			return ProjectionWindowResponse{}, err
		}
		boundary := indexOfTurn(turns, cursor.AnchorTurnID)
		if boundary < 0 {
			return ProjectionWindowResponse{}, errProjectionWindowCursorStale
		}
		slice.start, slice.end = growWindowRange(sizes, limit, boundary+1, false)
		// Strict turn-chain order (R7): everything between boundary and the page start
		// is the boundary turn itself, so the chain never skips an unloaded turn.
		hasOlder = slice.start > 0
		hasNewer = slice.end < len(turns)
	case projectionWindowDirectionLocate:
		anchor := indexOfTurn(turns, params.AnchorTurnID)
		if anchor < 0 {
			// Unknown or outside retention: only honest fallback is a full
			// get_session_projection pull (R8 — never a nearest-neighbor window).
			return ProjectionWindowResponse{}, errProjectionWindowLocateOut
		}
		end := anchor + limit/2
		if end > len(turns)-1 {
			end = len(turns) - 1
		}
		start := end - limit + 1
		if start < 0 {
			start = 0
		}
		// Byte budget keeps the anchor inside: shrink the far side first.
		bytes := 0
		budget := maxWindowEncodedBytes - windowEncodingOverhead
		for index := start; index <= end; index++ {
			bytes += sizes[index]
		}
		for bytes > budget && start < anchor {
			bytes -= sizes[start]
			start++
		}
		for bytes > budget && end > anchor {
			bytes -= sizes[end]
			end--
		}
		slice.start, slice.end = start, end+1
		hasOlder = slice.start > 0
		hasNewer = slice.end < len(turns)
	default:
		return ProjectionWindowResponse{}, fmt.Errorf("unknown window direction %q", params.Direction)
	}

	if empty {
		slice.start, slice.end = 0, 0
		hasOlder, hasNewer = false, false
	}
	if !empty && !hasOlder && hasOlderUpstream && slice.start == 0 {
		// R11d honesty: the kernel front is not the session start when the producer
		// still holds an unexhausted upstream cursor. The nextOlderCursor anchors at
		// the current kernel front; the older walk hydrates from there.
		hasOlder = true
	}

	page := turns[slice.start:slice.end]
	if slice.end > slice.start {
		nextOlderAnchor = turns[slice.start].TurnID
		nextNewerAnchor = turns[slice.end-1].TurnID
	} else if !empty {
		// Zero-length older page pinned at the committed front. nextOlderCursor
		// (when the producer fact claims upstream) anchors at the kernel front;
		// the newer chain resumes at the walk anchor. head/tail stay unset.
		nextOlderAnchor = turns[0].TurnID
		nextNewerAnchor = pageEmptyNewerAnchor
	}

	generation := projectionWindowGeneration.Add(1)
	descriptor := ProjectionWindowDescriptor{
		WindowID:   fmt.Sprintf("pw:%s|%s|%s|%d", backendID, bridgeEpoch, sessionID, generation),
		Generation: generation,
		Coverage:   "window",
		HasOlder:   hasOlder,
		HasNewer:   hasNewer,
	}
	if empty {
		descriptor.Coverage = "full"
	} else {
		if slice.end > slice.start {
			head := turns[slice.start].TurnID
			tail := turns[slice.end-1].TurnID
			descriptor.HeadTurnID = &head
			descriptor.TailTurnID = &tail
		}
		if !hasOlder {
			descriptor.Coverage = "full"
		}
		if hasOlder {
			descriptor.NextOlderCursor = encodeProjectionWindowCursor(projectionWindowCursor{
				V: 1, BridgeEpoch: bridgeEpoch, BackendID: backendID, SessionID: sessionID,
				AnchorTurnID: nextOlderAnchor, Side: "o",
			})
		}
		if hasNewer {
			descriptor.NextNewerCursor = encodeProjectionWindowCursor(projectionWindowCursor{
				V: 1, BridgeEpoch: bridgeEpoch, BackendID: backendID, SessionID: sessionID,
				AnchorTurnID: nextNewerAnchor, Side: "n",
			})
		}
	}

	response := ProjectionWindowResponse{
		Window:  descriptor,
		Turns:   append([]TurnProjection(nil), page...),
		SyncRev: proj.SyncRev,
	}
	if !hasNewer && !empty {
		// Anchoring paragraph: resume{at_head} iff the window includes the committed tail.
		response.Resume = &ProjectionWindowResume{Kind: "at_head"}
	}
	return response, nil
}

func indexOfTurn(turns []TurnProjection, turnID string) int {
	for index, turn := range turns {
		if turn.TurnID == turnID {
			return index
		}
	}
	return -1
}
