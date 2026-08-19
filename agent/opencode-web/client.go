package opencodeweb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// generation identifies which official opencode serve API generation the
// endpoint speaks (design §3.2). The default implementation face is 1.18
// (un-prefixed routes, bare-array list responses); v2 (/api prefix + {data}
// envelopes) is probed and switched to only when detected.
type generation string

const (
	generationUnknown generation = ""
	generation118     generation = "1.18"
	generationV2      generation = "v2"
)

// Client is the pure HTTP client for the official `opencode serve` API. It
// binds nothing and spawns nothing; the managed server lifecycle stays with
// the Swift-side supervisor (design §4.2).
type Client struct {
	baseURL    string
	user       string
	pass       string
	authHeader string
	// httpClient carries a whole-request timeout for regular API calls.
	httpClient *http.Client
	// streamClient has NO timeout: http.Client.Timeout covers reading the
	// response BODY, so any finite value would kill the SSE stream mid-turn
	// (owner-verified 2026-08-19: turn 2 died at the 30s mark mid-stream).
	streamClient *http.Client
	gen          generation
}

func newClient(baseURL, user, pass string) *Client {
	auth := ""
	if user != "" || pass != "" {
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	return &Client{
		baseURL:      strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		user:         user,
		pass:         pass,
		authHeader:   auth,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		streamClient: &http.Client{},
	}
}

// setGeneration pins the API generation discovered by the probe.
func (c *Client) setGeneration(g generation) { c.gen = g }

// Generation returns the pinned API generation.
func (c *Client) Generation() generation { return c.gen }

// apiPath maps an un-prefixed 1.18 route onto the v2 /api namespace. Routes
// that differ semantically between generations (prompt/abort/permission
// reply/activity) get dedicated helpers instead of this prefix map.
func (c *Client) apiPath(p string) string {
	if c.gen == generationV2 {
		return "/api" + p
	}
	return p
}

// endpoint returns the absolute URL for a path.
func (c *Client) endpoint(path string) string { return c.baseURL + path }

// doRequest performs one HTTP call. directory is sent as the
// x-opencode-directory header when non-empty (per-session directory for reads,
// current work dir for lists — design §2.1 坑 5). body (if non-nil) is
// JSON-encoded. auth controls whether the Basic Auth header is attached (the
// probe intentionally issues its first health call without it).
func (c *Client) doRequest(ctx context.Context, method, url string, body any, directory string, auth bool) (int, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth && c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	if directory != "" {
		// Official client shape: EVERY v1 SDK method carries directory as a
		// query parameter (sdk/js gen types.gen.ts — SessionListData /
		// SessionCreateData / SessionPromptData / SessionMessagesData /
		// ProviderListData / ProjectListData … all `query?: { directory?: string }`).
		// The x-opencode-directory header rides along as redundancy; the query
		// param is the canonical form (live probe: both scope 1.18.18).
		q := req.URL.Query()
		appendQuery := true
		if c.gen == generationV2 {
			// v2 SDK (v2/gen types.gen.ts, 2026-08-19 audit): ONLY
			// V2SessionListData declares query.directory — every other v2
			// route is `query?: never`. The official v2 client never sends
			// it elsewhere, and the x-opencode-directory header is a 1.18
			// convention that stays off the v2 wire.
			appendQuery = req.Method == http.MethodGet && strings.HasSuffix(url, "/api/session")
		}
		if appendQuery && q.Get("directory") == "" {
			q.Set("directory", directory)
			req.URL.RawQuery = q.Encode()
		}
		if c.gen != generationV2 {
			req.Header.Set("x-opencode-directory", directory)
		}
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

// fetchJSON GETs an API path. Non-2xx bodies flow into the error text
// verbatim (up to a bound) — HTTP failures must stay diagnosable, never fold
// into a generic error (design §2.2 纪律 4).
func (c *Client) fetchJSON(ctx context.Context, path, directory string) (json.RawMessage, error) {
	code, raw, err := c.doRequest(ctx, http.MethodGet, c.endpoint(path), nil, directory, true)
	if err != nil {
		return nil, err
	}
	if code >= 400 {
		return nil, fmt.Errorf("opencode-web HTTP %d: %s", code, truncateForError(string(raw)))
	}
	return raw, nil
}

// truncateForError bounds error-embedded body text by runes.
func truncateForError(s string) string {
	const max = 300
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// decodeJSONObject decodes an object payload (used by the activity map).
func decodeJSONObject(raw []byte, out any) error {
	return json.Unmarshal(raw, out)
}
