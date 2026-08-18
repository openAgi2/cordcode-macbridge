package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// probe.go implements the startup API-generation probe (design §3.2):
//
//  1. GET {base}/global/health — first without auth; on 401 retry with the
//     configured Basic Auth. The route exists only on the 1.18 generation
//     (checkout v2 serves health under /api), so an answer here is the
//     mutual-exclusion signal even though 1.18.18 also answers /api/health.
//  2. 404 / non-JSON → try GET {base}/api/health (v2 candidate).
//  3. GET …/session or /api/session — bare array vs {data} envelope is the
//     final shape arbiter.
//
// Every failure records each attempted path so diagnostics can state exactly
// what was tried. The probe never falls back to any hard-coded legacy port.

// probeTimeout bounds the whole probe sequence (loopback server).
const probeTimeout = 3 * time.Second

// probeResult is one probe outcome. err == nil means the endpoint is usable
// and gen names the detected generation.
type probeResult struct {
	gen    generation
	detail string
	err    error
	at     time.Time
}

type healthOutcome int

const (
	healthOK           healthOutcome = iota // 200 with healthy JSON (authed retry when challenged)
	healthNoAuthOK                          // 200 WITHOUT auth → unauthenticated server → rejected
	healthNotFound                          // 404 or non-JSON body → route missing
	healthAuthRejected                      // 401 with credentials (or 401 without credentials configured)
	healthFailed                            // transport error / other status / unhealthy payload
)

// probeHealth probes one health route and reports the outcome plus a short
// diagnostic fragment. The first request is unauthenticated: a server that
// answers 200 to it is judged server_unauthenticated and rejected (the
// managed-local server always carries Basic Auth; there is no legacy-port
// exception in this package).
func probeHealth(ctx context.Context, c *Client, path string) (healthOutcome, string) {
	code, raw, err := c.doRequest(ctx, "GET", c.endpoint(path), nil, "", false)
	if err != nil {
		return healthFailed, fmt.Sprintf("%s: %v", path, err)
	}
	switch {
	case code == 401:
		if c.authHeader == "" {
			return healthAuthRejected, fmt.Sprintf("%s: 401 and no credentials configured", path)
		}
		code2, raw2, err2 := c.doRequest(ctx, "GET", c.endpoint(path), nil, "", true)
		if err2 != nil {
			return healthFailed, fmt.Sprintf("%s (authed): %v", path, err2)
		}
		if code2 == 200 {
			if healthyPayload(raw2) {
				return healthOK, fmt.Sprintf("%s: 200 (authed)", path)
			}
			return healthFailed, fmt.Sprintf("%s (authed): 200 but payload not healthy JSON", path)
		}
		if code2 == 404 {
			return healthNotFound, fmt.Sprintf("%s (authed): 404", path)
		}
		return healthAuthRejected, fmt.Sprintf("%s (authed): %d %s", path, code2, truncateForError(string(raw2)))
	case code == 200:
		return healthNoAuthOK, fmt.Sprintf("%s: 200 without authentication", path)
	case code == 404:
		return healthNotFound, fmt.Sprintf("%s: 404", path)
	default:
		if !looksLikeJSON(raw) {
			return healthNotFound, fmt.Sprintf("%s: %d non-JSON", path, code)
		}
		return healthFailed, fmt.Sprintf("%s: %d %s", path, code, truncateForError(string(raw)))
	}
}

// healthyPayload accepts {"healthy":true} and empty bodies (1.18 health
// variations) — the probe's job is route/authentication discovery, not deep
// payload validation.
func healthyPayload(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return true
	}
	var body struct {
		Healthy *bool `json:"healthy"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false
	}
	return body.Healthy == nil || *body.Healthy
}

func looksLikeJSON(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// sessionShape reports whether the session route answers a bare array (1.18)
// or a {data} envelope (v2).
type sessionShape int

const (
	shapeBareArray sessionShape = iota
	shapeDataEnvelope
	shapeUnknown
)

func probeSessionShape(ctx context.Context, c *Client, path string) (sessionShape, string) {
	code, raw, err := c.doRequest(ctx, "GET", c.endpoint(path), nil, "", true)
	if err != nil {
		return shapeUnknown, fmt.Sprintf("%s: %v", path, err)
	}
	if code >= 400 {
		return shapeUnknown, fmt.Sprintf("%s: %d %s", path, code, truncateForError(string(raw)))
	}
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "["):
		return shapeBareArray, fmt.Sprintf("%s: bare array", path)
	case strings.HasPrefix(trimmed, "{"):
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Data != nil {
			return shapeDataEnvelope, fmt.Sprintf("%s: {data} envelope", path)
		}
		return shapeUnknown, fmt.Sprintf("%s: JSON object without data envelope", path)
	default:
		return shapeUnknown, fmt.Sprintf("%s: non-JSON body", path)
	}
}

// probeInstance runs the full design-§3.2 sequence. It mutates nothing on the
// server (GET only).
func probeInstance(ctx context.Context, c *Client) probeResult {
	var tried []string

	legacyHealth, legacyDetail := probeHealth(ctx, c, "/global/health")
	tried = append(tried, legacyDetail)

	candidate := generationUnknown
	switch legacyHealth {
	case healthNoAuthOK:
		return probeResult{err: fmt.Errorf("endpoint refuses nothing: %s (server_unauthenticated)", legacyDetail), at: time.Now()}
	case healthOK:
		candidate = generation118
	case healthNotFound:
		v2Health, v2Detail := probeHealth(ctx, c, "/api/health")
		tried = append(tried, v2Detail)
		switch v2Health {
		case healthNoAuthOK:
			return probeResult{err: fmt.Errorf("endpoint refuses nothing: %s (server_unauthenticated)", v2Detail), at: time.Now()}
		case healthOK:
			candidate = generationV2
		default:
			return probeResult{err: fmt.Errorf("no usable health route (tried: %s)", strings.Join(tried, "; ")), at: time.Now()}
		}
	default:
		return probeResult{err: fmt.Errorf("health probe failed (tried: %s)", strings.Join(tried, "; ")), at: time.Now()}
	}

	// Final shape arbiter on the candidate generation's session route.
	shapePath := "/session"
	expected := shapeBareArray
	if candidate == generationV2 {
		shapePath = "/api/session"
		expected = shapeDataEnvelope
	}
	shape, shapeDetail := probeSessionShape(ctx, c, shapePath)
	tried = append(tried, shapeDetail)
	if shape == shapeUnknown || shape != expected {
		// Conflicting signals: try the other generation's session route and
		// let whichever matches win; if neither matches, fail with both.
		altPath := "/api/session"
		altExpected := shapeDataEnvelope
		if candidate == generationV2 {
			altPath = "/session"
			altExpected = shapeBareArray
		}
		altShape, altDetail := probeSessionShape(ctx, c, altPath)
		tried = append(tried, altDetail)
		if altShape != shapeUnknown && altShape == altExpected {
			if candidate == generation118 {
				candidate = generationV2
			} else {
				candidate = generation118
			}
		} else {
			return probeResult{err: fmt.Errorf("session shape probe failed (tried: %s)", strings.Join(tried, "; ")), at: time.Now()}
		}
	}

	return probeResult{
		gen:    candidate,
		detail: fmt.Sprintf("generation=%s url=%s", candidate, c.baseURL),
		at:     time.Now(),
	}
}
