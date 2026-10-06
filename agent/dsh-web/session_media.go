package dshweb

// Session Markdown image media read (plan 2026-10-05-dsh-session-markdown-
// image-display §5.1/§5.2): resolves the session cwd from backend session
// truth, contains the authored path, and reads the bytes through the
// official authenticated GET /api/file route (dsh-v0.1.7-rc.2
// packages/api/session-controller/src/media-references.ts serveFile). The
// provider is the only read path — never os.ReadFile, which would bypass the
// DSH filesystem provider and diverge from the official Web's semantics.
//
// Error mapping (plan §4.4): the official signal is HTTP status + body text
// (FsError code for provider failures, plain text for the 400 pre-checks).
// FS_NOT_REGULAR_FILE / FS_PERMISSION_DENIED / FS_SANDBOX_DENIED all return
// 403, so the body must be read to keep the codes distinguishable.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// sessionMediaMaxBytes mirrors the official attachment image limit default
// (attachment-local DEFAULT_MAX_IMAGE_BYTES = 20 MiB; plan §4.5). The provider
// enforces its own limit first (413 FS_TOO_LARGE); the +1 bounded read below
// is the local backstop for seats configured with a higher limit.
const sessionMediaMaxBytes int64 = 20 << 20

// sessionMediaAllowedMIME is the Phase 1 image whitelist (plan §4.5; SVG
// added 2026-10-05 owner acceptance round: official /api/file serves
// image/svg+xml and the official Web <img> renders it — parity gap. iOS
// rasterizes SVG client-side via WebKit; the bridge only carries bytes).
var sessionMediaAllowedMIME = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"image/gif":       true,
	"image/svg+xml":   true,
}

var _ core.SessionMediaReader = (*Agent)(nil)

// ReadSessionMedia implements core.SessionMediaReader.
func (a *Agent) ReadSessionMedia(ctx context.Context, sessionID, authoredPath string) (*core.SessionMedia, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, &core.SessionMediaError{Code: core.SessionMediaInvalidParams, Detail: "empty session id"}
	}
	if authoredPath == "" {
		return nil, &core.SessionMediaError{Code: core.SessionMediaInvalidParams, Detail: "empty path"}
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, &core.SessionMediaError{Code: core.SessionMediaTransportFailed, Detail: fmt.Sprintf("seat resolve: %v", err)}
	}
	cwd, err := a.sessionCwdFor(ctx, client, sessionID)
	if err != nil {
		return nil, err
	}
	if err := validateSessionMediaPath(authoredPath); err != nil {
		return nil, err
	}
	resolved, err := resolveWithinSessionCwd(cwd, authoredPath)
	if err != nil {
		return nil, err
	}
	contentType, data, err := client.ReadFile(ctx, resolved, sessionMediaMaxBytes)
	if err != nil {
		return nil, mapSessionMediaProviderError(err)
	}
	mediaType, _, perr := mime.ParseMediaType(contentType)
	if perr != nil || !sessionMediaAllowedMIME[mediaType] {
		return nil, &core.SessionMediaError{
			Code:   core.SessionMediaUnsupportedMediaType,
			Detail: fmt.Sprintf("provider Content-Type %q is not an allowed image type", contentType),
		}
	}
	sum := sha256.Sum256(data)
	return &core.SessionMedia{
		ResolvedPath:  resolved,
		MediaType:     mediaType,
		Data:          data,
		ContentSha256: hex.EncodeToString(sum[:]),
	}, nil
}

// sessionCwdFor resolves the session's cwd from backend session truth — the
// session/list row's Cwd, the same source the official ChatView session store
// reads (plan §5.2 item 1). It must NOT use the workspace grouping path or
// the ungrouped sentinel: those are presentation groupings, not the
// resolution root (F-B1; think.md 2026-08-16/17 cwd-冒充分组-directory 事故面).
func (a *Agent) sessionCwdFor(ctx context.Context, client *Client, sessionID string) (string, error) {
	var val sessionListValue
	if err := client.Call(ctx, "session/list", map[string]any{"_request": sessionListRequest{}}, &val); err != nil {
		return "", &core.SessionMediaError{Code: core.SessionMediaTransportFailed, Detail: fmt.Sprintf("session/list: %v", err)}
	}
	for _, item := range val.Items {
		if item.SessionID != sessionID {
			continue
		}
		if strings.TrimSpace(item.Cwd) == "" {
			return "", &core.SessionMediaError{Code: core.SessionMediaInvalidParams, Detail: "session cwd unavailable in session truth"}
		}
		return item.Cwd, nil
	}
	return "", &core.SessionMediaError{Code: core.SessionMediaSessionNotFound, Detail: "session not present in session/list"}
}

// validateSessionMediaPath applies the Phase 1 lexical rules (plan §4.2): the
// authored destination must be a relative path — no URL scheme, no
// protocol-relative form, no absolute path, no NUL/control characters. The
// iOS client preflights the same rules; this is the bridge-side re-check.
func validateSessionMediaPath(p string) error {
	invalid := func(detail string) error {
		return &core.SessionMediaError{Code: core.SessionMediaInvalidParams, Detail: detail}
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return invalid("control character in path")
		}
	}
	lower := strings.ToLower(p)
	for _, scheme := range []string{"file:", "http:", "https:"} {
		if strings.HasPrefix(lower, scheme) {
			return invalid("URL scheme not allowed: " + scheme)
		}
	}
	if strings.HasPrefix(p, "//") {
		return invalid("protocol-relative path not allowed")
	}
	if filepath.IsAbs(p) {
		return invalid("absolute path not allowed in Phase 1")
	}
	return nil
}

// resolveWithinSessionCwd joins the authored relative path onto the session
// cwd and enforces Phase 1 containment (plan §5.2 item 2): lexical
// containment first; when the target exists, EvalSymlinks re-checks the
// RESOLVED path against the resolved cwd so an in-cwd symlink pointing
// outside is rejected (media.path_escape). When the target does not exist,
// EvalSymlinks fails and the lexical verdict stands — the provider then
// answers FS_NOT_FOUND, keeping the official missing-file semantics.
func resolveWithinSessionCwd(cwd, authoredPath string) (string, error) {
	base := filepath.Clean(cwd)
	lexAbs := filepath.Clean(filepath.Join(base, authoredPath))
	if !withinSessionBase(lexAbs, base) {
		return "", &core.SessionMediaError{
			Code:   core.SessionMediaPathEscape,
			Detail: fmt.Sprintf("%q escapes session cwd %q", authoredPath, base),
		}
	}
	resolved, rerr := filepath.EvalSymlinks(lexAbs)
	if rerr != nil {
		// Target (or an ancestor) is missing: the lexical verdict stands and
		// the provider reports the missing file.
		return lexAbs, nil
	}
	resolvedBase, berr := filepath.EvalSymlinks(base)
	if berr != nil {
		resolvedBase = base
	}
	if !withinSessionBase(resolved, resolvedBase) {
		return "", &core.SessionMediaError{
			Code:   core.SessionMediaPathEscape,
			Detail: fmt.Sprintf("symlink target %q escapes session cwd %q", resolved, resolvedBase),
		}
	}
	return resolved, nil
}

// withinSessionBase reports whether p is base itself or a descendant of it.
func withinSessionBase(p, base string) bool {
	if p == base {
		return true
	}
	return strings.HasPrefix(p, base+string(filepath.Separator))
}

// errSessionMediaOversize marks a 200 /api/file response whose body exceeded
// the caller's byte limit (the provider's own limit normally 413s first;
// this is the local backstop for seats configured with a higher limit).
var errSessionMediaOversize = errors.New("session media response exceeded byte limit")

// ReadFile performs the official authenticated raw-byte GET
// /api/file?path=<abs> (media-references.ts serveFile): 200 answers the file
// bytes with the provider's Content-Type; every non-200 status carries the
// official error text in the body (FsError code for provider failures, plain
// text for the 400 pre-checks) and is returned as *carrierError{Status,
// Detail} for the caller to map. A 401 triggers one cookie refresh + retry.
func (c *Client) ReadFile(ctx context.Context, absPath string, maxBytes int64) (string, []byte, error) {
	contentType, data, err := c.readFileOnce(ctx, absPath, maxBytes)
	if c.retryOnAuth(ctx, err) {
		contentType, data, err = c.readFileOnce(ctx, absPath, maxBytes)
	}
	return contentType, data, err
}

func (c *Client) readFileOnce(ctx context.Context, absPath string, maxBytes int64) (string, []byte, error) {
	contentType, data, err := c.readBodyOnce(ctx, absPath, maxBytes+1)
	if err != nil {
		return "", nil, err
	}
	if int64(len(data)) > maxBytes {
		return "", nil, errSessionMediaOversize
	}
	return contentType, data, nil
}

// readBodyOnce performs the official authenticated GET /api/file and reads
// at most limit bytes of the body (shared by the full read — limit+1 so the
// caller can detect oversize — and the dimension probe's head read).
func (c *Client) readBodyOnce(ctx context.Context, absPath string, limit int64) (string, []byte, error) {
	u := c.BaseURL + "/api/file?path=" + url.QueryEscape(absPath)
	ctx, cancel := unaryCtx(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", nil, &carrierError{Op: "api/file", Wrapped: err}
	}
	c.applyCookie(req.Header)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", nil, &carrierError{Op: "api/file", Wrapped: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", nil, &carrierError{Op: "api/file", Status: resp.StatusCode, Detail: strings.TrimSpace(string(body))}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return "", nil, &carrierError{Op: "api/file", Status: resp.StatusCode, Wrapped: err}
	}
	return resp.Header.Get("Content-Type"), data, nil
}

// mapSessionMediaProviderError maps an /api/file failure onto the stable
// media codes (plan §4.4 table). The official signal is HTTP status + body
// text: FS_NOT_REGULAR_FILE / FS_PERMISSION_DENIED / FS_SANDBOX_DENIED all
// return 403, so the body decides the code. Unknown status/body combinations
// fail closed to media.transport_failed with the official detail preserved.
func mapSessionMediaProviderError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errSessionMediaOversize) {
		return &core.SessionMediaError{Code: core.SessionMediaTooLarge, Detail: "local bounded read exceeded the image byte limit"}
	}
	var ce *carrierError
	if errors.As(err, &ce) {
		if ce.Status == 0 {
			return &core.SessionMediaError{Code: core.SessionMediaTransportFailed, Detail: ce.Error()}
		}
		body := strings.TrimSpace(ce.Detail)
		switch ce.Status {
		case http.StatusBadRequest:
			// Official pre-checks: 'missing path', 'absolute path required'.
			return &core.SessionMediaError{Code: core.SessionMediaInvalidParams, Detail: body}
		case http.StatusNotFound:
			if body == "FS_NOT_FOUND" {
				return &core.SessionMediaError{Code: core.SessionMediaNotFound, Detail: body}
			}
		case http.StatusForbidden:
			switch body {
			case "FS_NOT_REGULAR_FILE":
				return &core.SessionMediaError{Code: core.SessionMediaNotRegularFile, Detail: body}
			case "FS_PERMISSION_DENIED", "FS_SANDBOX_DENIED":
				return &core.SessionMediaError{Code: core.SessionMediaPermissionDenied, Detail: body}
			}
		case http.StatusRequestEntityTooLarge:
			if body == "FS_TOO_LARGE" {
				return &core.SessionMediaError{Code: core.SessionMediaTooLarge, Detail: body}
			}
		case 499:
			if body == "FS_ABORTED" {
				return &core.SessionMediaError{Code: core.SessionMediaTransportFailed, Detail: body}
			}
		}
		return &core.SessionMediaError{Code: core.SessionMediaTransportFailed, Detail: fmt.Sprintf("HTTP %d: %s", ce.Status, body)}
	}
	return &core.SessionMediaError{Code: core.SessionMediaTransportFailed, Detail: err.Error()}
}
