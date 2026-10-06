package gobridge

// session_media_protocol_test.go — §5.4 test 11 (protocol schema/sample
// sync): the docs/protocol/samples/session-media fixtures decode against the
// handler's param/result shapes, the result's base64/bytes/SHA-256 stay
// mutually consistent, the request carries no root field (F-B1), and the
// error fixture rides the standard result envelope with the stable media.*
// code. The canonical↔iOS mirror byte diff is the A6 delivery check.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSessionMediaProtocolSamples(t *testing.T) {
	dir := "../docs/protocol/samples/session-media"

	// Request sample: decodes into GetSessionMediaParams and carries NO root
	// field — a directory/root key reappearing here would reopen the F-B1
	// mis-wiring surface the plan explicitly closed.
	raw, err := os.ReadFile(dir + "/get-session-media-request.json")
	if err != nil {
		t.Fatal(err)
	}
	var params GetSessionMediaParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	if params.SessionID == "" || params.Path == "" {
		t.Fatalf("sample request = %+v", params)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"directory", "root", "workspaceRoot", "cwd"} {
		if _, ok := keys[banned]; ok {
			t.Fatalf("request sample must not carry a root field %q (F-B1)", banned)
		}
	}

	// Result sample: base64 decodes, byte count and SHA-256 stay consistent,
	// mediaType is one of the four allowed image types.
	raw, err = os.ReadFile(dir + "/get-session-media-result.json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		ResolvedPath  string `json:"resolvedPath"`
		MediaType     string `json:"mediaType"`
		Bytes         int    `json:"bytes"`
		ContentSha256 string `json:"contentSha256"`
		Data          string `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		t.Fatalf("sample data is not canonical base64: %v", err)
	}
	if len(data) != result.Bytes {
		t.Fatalf("bytes = %d, data length = %d", result.Bytes, len(data))
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != result.ContentSha256 {
		t.Fatalf("contentSha256 = %s, want %s", result.ContentSha256, hex.EncodeToString(sum[:]))
	}
	allowed := map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true, "image/svg+xml": true}
	if !allowed[result.MediaType] {
		t.Fatalf("mediaType = %q, want one of the allowed image types", result.MediaType)
	}
	if result.ResolvedPath == "" {
		t.Fatal("resolvedPath must be present")
	}

	// SVG result sample (2026-10-05-r1): same consistency invariants plus the
	// declared <svg> dimensions parse — the iOS client's rasterizer sizing
	// input. A fixture losing width/height/viewBox would break client sizing.
	raw, err = os.ReadFile(dir + "/get-session-media-result-svg.json")
	if err != nil {
		t.Fatal(err)
	}
	var svgResult struct {
		ResolvedPath  string `json:"resolvedPath"`
		MediaType     string `json:"mediaType"`
		Bytes         int    `json:"bytes"`
		ContentSha256 string `json:"contentSha256"`
		Data          string `json:"data"`
	}
	if err := json.Unmarshal(raw, &svgResult); err != nil {
		t.Fatal(err)
	}
	svgData, err := base64.StdEncoding.DecodeString(svgResult.Data)
	if err != nil {
		t.Fatalf("svg sample data is not canonical base64: %v", err)
	}
	if len(svgData) != svgResult.Bytes {
		t.Fatalf("svg bytes = %d, data length = %d", svgResult.Bytes, len(svgData))
	}
	svgSum := sha256.Sum256(svgData)
	if hex.EncodeToString(svgSum[:]) != svgResult.ContentSha256 {
		t.Fatalf("svg contentSha256 = %s, want %s", svgResult.ContentSha256, hex.EncodeToString(svgSum[:]))
	}
	if svgResult.MediaType != "image/svg+xml" {
		t.Fatalf("svg mediaType = %q, want image/svg+xml", svgResult.MediaType)
	}
	svgSource := string(svgData)
	if !strings.Contains(svgSource, `width="`) || !strings.Contains(svgSource, `height="`) {
		if !strings.Contains(svgSource, `viewBox="`) {
			t.Fatal("svg fixture must declare width/height or viewBox (client rasterizer sizing input)")
		}
	}

	// Error sample: the stable media.* code rides the standard result envelope.
	raw, err = os.ReadFile(dir + "/get-session-media-error-not-found.json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Error *WireError `json:"error"`
		OK    bool       `json:"ok"`
		Type  string     `json:"type"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK || env.Type != "result" || env.Error == nil {
		t.Fatalf("error envelope = %+v", env)
	}
	if env.Error.Code != core.SessionMediaNotFound {
		t.Fatalf("error code = %q, want %q", env.Error.Code, core.SessionMediaNotFound)
	}
}
