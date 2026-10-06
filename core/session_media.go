package core

import (
	"context"
)

// Session media stable error codes (DSH markdown image display plan §4.4).
// The bridge handler maps SessionMediaError.Code onto the wire error codes
// verbatim; Detail preserves the official provider signal (HTTP status +
// FsError body text) for logs and diagnostics — never rewritten or collapsed
// into a generic failure.
const (
	// SessionMediaInvalidParams: malformed request — empty session id, empty
	// path, absolute path, URL scheme, protocol-relative path, or control
	// characters (Phase 1 accepts relative paths only).
	SessionMediaInvalidParams = "media.invalid_params"
	// SessionMediaSessionNotFound: the session id is not in backend session truth.
	SessionMediaSessionNotFound = "media.session_not_found"
	// SessionMediaPathEscape: the resolved path escapes the session cwd
	// (lexical .. escape, or a symlink inside the cwd pointing outside).
	SessionMediaPathEscape = "media.path_escape"
	// SessionMediaNotFound: the provider reports the file missing
	// (404 / FS_NOT_FOUND).
	SessionMediaNotFound = "media.not_found"
	// SessionMediaNotRegularFile: the provider reports a non-file
	// (403 / FS_NOT_REGULAR_FILE).
	SessionMediaNotRegularFile = "media.not_regular_file"
	// SessionMediaPermissionDenied: the provider denies the read
	// (403 / FS_PERMISSION_DENIED or FS_SANDBOX_DENIED).
	SessionMediaPermissionDenied = "media.permission_denied"
	// SessionMediaTooLarge: the read exceeds the 20 MiB image byte limit
	// (413 / FS_TOO_LARGE, or the local bounded-read backstop).
	SessionMediaTooLarge = "media.too_large"
	// SessionMediaUnsupportedMediaType: the provider Content-Type is not one
	// of the four allowed image MIME types.
	SessionMediaUnsupportedMediaType = "media.unsupported_media_type"
	// SessionMediaTransportFailed: transport or unknown provider failure
	// (499 / FS_ABORTED, 5xx, unknown status/body, seat unreachable).
	SessionMediaTransportFailed = "media.transport_failed"
	// SessionMediaBackendNotSupported: the backend does not implement
	// SessionMediaReader — the bridge fails closed with this code when the
	// capability is absent.
	SessionMediaBackendNotSupported = "media.backend_not_supported"
)

// SessionMediaError is the stable, distinguishable error for session media
// reads: Code is one of the media.* constants; Detail carries the official
// provider signal verbatim (e.g. "HTTP 403: FS_PERMISSION_DENIED").
type SessionMediaError struct {
	Code   string
	Detail string
}

func (e *SessionMediaError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

// SessionMedia is one authenticated media read: the canonical absolute path
// actually read, the provider-reported MIME type, the raw bytes, and the
// lowercase-hex SHA-256 of those bytes. It deliberately carries no
// presentation model — decoding and display belong to the client.
type SessionMedia struct {
	ResolvedPath  string
	MediaType     string // image/png | image/jpeg | image/webp | image/gif
	Data          []byte
	ContentSha256 string
}

// SessionMediaReader is an optional interface for agents that can read
// workspace-relative media files referenced by a session's finalized
// assistant Markdown through the backend's AUTHENTICATED file channel
// (dsh-web: official GET /api/file through the DSH filesystem provider —
// media-references.ts). The resolution root, the session cwd, is resolved by
// the driver from backend session truth (the session/list row's cwd, the
// same source the official ChatView session store reads). Callers must NOT
// pass any root/directory hint: a workspace grouping path or a
// client-declared root must never masquerade as the resolution root.
//
// Implementations must:
//   - reject absolute paths, URL schemes, protocol-relative paths, and
//     control characters, and contain the resolved path within the session
//     cwd (symlink-evaluated when the target exists);
//   - read through the official provider — never bypass it with local file
//     reads — and map provider errors onto the media.* codes with the
//     official detail preserved.
type SessionMediaReader interface {
	ReadSessionMedia(ctx context.Context, sessionID, authoredPath string) (*SessionMedia, error)
}

// SessionMediaDimensions is one dimension probe result: the provider-reported
// MIME type and the intrinsic pixel size (SVG: declared size). It is a layout
// HINT for the client's row-height pipeline — the authoritative dimensions
// remain whatever the client parses from the full media bytes on read.
type SessionMediaDimensions struct {
	MediaType string
	Width     int
	Height    int
}

// SessionMediaDimensionProber is an optional interface for agents that can
// batch-probe the intrinsic dimensions of session media files WITHOUT the
// client transferring the full bytes. The probe exists so the client can
// reserve the exact row height before the image pixels arrive (the
// height-jump fix: a 44pt loading placeholder flipping to the real image
// height mid-scroll is a visible jump; knowing the dimensions up front makes
// the height final from the first layout).
//
// Semantics (deliberately hint-shaped, fail-soft per path):
//   - the map key is the AUTHORED path exactly as passed in (no
//     percent-decoding — the caller sends the already-decoded send shape,
//     same as get_session_media);
//   - a path that fails validation, escapes the cwd, is missing, is not an
//     allowed image type, or whose dimensions cannot be parsed from the head
//     bytes is simply ABSENT from the map — per-path failures never fail the
//     RPC (the client falls back to sizing at byte arrival);
//   - session-level failures (empty session id, session not in backend
//     truth, transport) return an error mapped onto the media.* codes.
//
// Reads go through the same authenticated provider route as
// SessionMediaReader (head-limited, never a local file read bypass).
type SessionMediaDimensionProber interface {
	ProbeSessionMediaDimensions(
		ctx context.Context, sessionID string, authoredPaths []string,
	) (map[string]*SessionMediaDimensions, error)
}
