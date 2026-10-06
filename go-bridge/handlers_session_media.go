package gobridge

// handlers_session_media.go — DSH session Markdown image display plan
// §4/§5.3: get_session_media RPC. Reads one workspace-relative media file
// referenced by a session's finalized assistant Markdown through the
// backend's authenticated file channel (dsh-web /api/file). The request
// carries NO root/directory field — the resolution root (session cwd) is
// resolved by the driver from backend session truth, mirroring the official
// ChatView resolve closure. The read is a lazy per-request fetch: nothing
// enters the projection/event stream, and read_file_v2's bulk/cancel
// semantics are deliberately not reused.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleGetSessionMedia(conn Connection, msg WireMessage, agent core.Agent) {
	// Capability gate: only backends implementing core.SessionMediaReader
	// advertise session_media_read; the RPC fails closed for everyone else.
	reader, ok := agent.(core.SessionMediaReader)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    core.SessionMediaBackendNotSupported,
			Message: "backend does not support session media reads",
		})
		return
	}
	var params GetSessionMediaParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: core.SessionMediaInvalidParams, Message: "sessionId required"})
		return
	}
	if params.Path == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: core.SessionMediaInvalidParams, Message: "path required"})
		return
	}
	// A 20 MiB image base64s to ≈26.7 MiB on the wire; keep the attachment
	// read's 60s window so a slow provider read surfaces as a timeout rather
	// than hanging the client.
	ctx, cancel := context.WithTimeout(h.ctx, 60*time.Second)
	defer cancel()
	media, err := reader.ReadSessionMedia(ctx, params.SessionID, params.Path)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, sessionMediaWireError(err))
		return
	}
	conn.SendResult(msg.RequestID, map[string]any{
		"resolvedPath":  media.ResolvedPath,
		"mediaType":     media.MediaType,
		"bytes":         len(media.Data),
		"contentSha256": media.ContentSha256,
		"data":          base64.StdEncoding.EncodeToString(media.Data),
	}, nil)
}

// sessionMediaDimensionsMaxPaths caps one probe batch: a page's worth of
// standalone image blocks (iOS chunks larger batches itself).
const sessionMediaDimensionsMaxPaths = 64

// handleGetSessionMediaDimensions (height-jump fix 2026-10-06): batch
// dimension probe over the session cwd's media files so the client can
// reserve exact row heights before the image bytes arrive. Per-path
// failures are ABSENT from the result map (hint semantics — the read RPC
// remains the authoritative failure surface); only session-level failures
// error out with the stable media.* codes.
func (h *Handlers) handleGetSessionMediaDimensions(conn Connection, msg WireMessage, agent core.Agent) {
	// Capability gate: only backends implementing core.SessionMediaDimensionProber
	// advertise session_media_dimensions; the RPC fails closed for everyone else.
	prober, ok := agent.(core.SessionMediaDimensionProber)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    core.SessionMediaBackendNotSupported,
			Message: "backend does not support session media dimension probes",
		})
		return
	}
	var params GetSessionMediaDimensionsParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: core.SessionMediaInvalidParams, Message: "sessionId required"})
		return
	}
	if len(params.Paths) == 0 {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: core.SessionMediaInvalidParams, Message: "paths required"})
		return
	}
	if len(params.Paths) > sessionMediaDimensionsMaxPaths {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    core.SessionMediaInvalidParams,
			Message: fmt.Sprintf("too many paths in one probe (max %d)", sessionMediaDimensionsMaxPaths),
		})
		return
	}
	// A 64-path batch of head reads stays well under the read RPC's window;
	// keep the same generous ceiling so a slow provider surfaces as a timeout.
	ctx, cancel := context.WithTimeout(h.ctx, 60*time.Second)
	defer cancel()
	dimensions, err := prober.ProbeSessionMediaDimensions(ctx, params.SessionID, params.Paths)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, sessionMediaWireError(err))
		return
	}
	out := make(map[string]map[string]any, len(dimensions))
	for path, dims := range dimensions {
		out[path] = map[string]any{
			"mediaType": dims.MediaType,
			"width":     dims.Width,
			"height":    dims.Height,
		}
	}
	conn.SendResult(msg.RequestID, map[string]any{"dimensions": out}, nil)
}

// sessionMediaWireError maps a media read failure onto the wire error
// (plan §4.4: the stable media.* code plus the official detail preserved
// verbatim). Non-typed driver errors fail closed to media.transport_failed.
func sessionMediaWireError(err error) *WireError {
	var me *core.SessionMediaError
	if errors.As(err, &me) {
		message := me.Detail
		if message == "" {
			message = me.Code
		}
		return &WireError{Code: me.Code, Message: message}
	}
	return &WireError{Code: core.SessionMediaTransportFailed, Message: err.Error()}
}
