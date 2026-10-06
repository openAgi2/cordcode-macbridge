package dshweb

// Session media dimension probe (height-jump fix, 2026-10-06 owner round 6):
// batch-probe intrinsic image dimensions so the iOS client can reserve the
// exact row height BEFORE the image bytes arrive. The probe reads only the
// file head through the same official authenticated /api/file route as
// ReadSessionMedia (never a local file read bypass) and parses the header:
// PNG IHDR, JPEG SOF, GIF logical screen, WebP VP8X canvas, and SVG declared
// size (mirroring the iOS SessionSVGSupport.declaredSize semantics — width/
// height absolute attributes first, viewBox fallback).
//
// The result is a layout HINT: per-path failures (validation, escape,
// missing, non-image, unparseable head) leave the path absent from the map —
// the client falls back to sizing at byte arrival. Only session-level
// failures error out (media.* codes).

import (
	"context"
	"encoding/binary"
	"fmt"
	"mime"
	"strconv"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// sessionMediaDimensionHeadBytes caps the head read: PNG/GIF/WebP headers
// are within the first 30 bytes, JPEG SOF follows the EXIF block (well under
// 512 KiB in practice), and the SVG root tag sits at the top of the source.
// A file whose header cannot be found within the cap is absent from the map
// (graceful — the client sizes at byte arrival).
const sessionMediaDimensionHeadBytes int64 = 512 << 10

var _ core.SessionMediaDimensionProber = (*Agent)(nil)

// ProbeSessionMediaDimensions implements core.SessionMediaDimensionProber.
func (a *Agent) ProbeSessionMediaDimensions(
	ctx context.Context, sessionID string, authoredPaths []string,
) (map[string]*core.SessionMediaDimensions, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, &core.SessionMediaError{Code: core.SessionMediaInvalidParams, Detail: "empty session id"}
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, &core.SessionMediaError{Code: core.SessionMediaTransportFailed, Detail: fmt.Sprintf("seat resolve: %v", err)}
	}
	cwd, err := a.sessionCwdFor(ctx, client, sessionID)
	if err != nil {
		return nil, err
	}
	dimensions := make(map[string]*core.SessionMediaDimensions, len(authoredPaths))
	for _, authored := range authoredPaths {
		// Per-path fail-soft: any rejection leaves the path absent (hint
		// semantics — the read path remains the authoritative failure
		// surface with its stable media.* errors).
		if authored == "" {
			continue
		}
		if err := validateSessionMediaPath(authored); err != nil {
			continue
		}
		resolved, err := resolveWithinSessionCwd(cwd, authored)
		if err != nil {
			continue
		}
		contentType, head, err := client.ReadFileHead(ctx, resolved, sessionMediaDimensionHeadBytes)
		if err != nil {
			continue
		}
		mediaType, _, perr := mime.ParseMediaType(contentType)
		if perr != nil || !sessionMediaAllowedMIME[mediaType] {
			continue
		}
		if w, h, ok := parseImageDimensions(mediaType, head); ok {
			dimensions[authored] = &core.SessionMediaDimensions{
				MediaType: mediaType,
				Width:     w,
				Height:    h,
			}
		}
	}
	return dimensions, nil
}

// ReadFileHead reads at most maxBytes of the official authenticated
// /api/file body. The provider still streams the whole file; the probe
// consumes only the head and closes early. Non-200 statuses carry the
// official error text (same carrierError shape as ReadFile); a 401 triggers
// one cookie refresh + retry.
func (c *Client) ReadFileHead(ctx context.Context, absPath string, maxBytes int64) (string, []byte, error) {
	contentType, data, err := c.readBodyOnce(ctx, absPath, maxBytes)
	if c.retryOnAuth(ctx, err) {
		contentType, data, err = c.readBodyOnce(ctx, absPath, maxBytes)
	}
	return contentType, data, err
}

// parseImageDimensions routes by provider media type: SVG is text (no magic
// bytes — the Content-Type decides); binary formats are sniffed by magic so
// provider Content-Type quirks cannot misroute the header layout.
func parseImageDimensions(mediaType string, head []byte) (int, int, bool) {
	if mediaType == "image/svg+xml" {
		return svgDeclaredSize(head)
	}
	return binaryImageDimensions(head)
}

func binaryImageDimensions(head []byte) (int, int, bool) {
	if w, h, ok := pngDimensions(head); ok {
		return w, h, true
	}
	if w, h, ok := jpegDimensions(head); ok {
		return w, h, true
	}
	if w, h, ok := gifDimensions(head); ok {
		return w, h, true
	}
	if w, h, ok := webpDimensions(head); ok {
		return w, h, true
	}
	return 0, 0, false
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// pngDimensions: IHDR is the mandatory first chunk — width/height are
// big-endian uint32 at fixed offsets 16..23.
func pngDimensions(head []byte) (int, int, bool) {
	if len(head) < 24 || string(head[:8]) != string(pngSignature) {
		return 0, 0, false
	}
	if string(head[12:16]) != "IHDR" {
		return 0, 0, false
	}
	w := int(binary.BigEndian.Uint32(head[16:20]))
	h := int(binary.BigEndian.Uint32(head[20:24]))
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// jpegDimensions scans SOF markers (C0..CF minus C4/C8/CC — the variants
// that carry dimensions) after the SOI, skipping EXIF and other segments.
func jpegDimensions(head []byte) (int, int, bool) {
	if len(head) < 4 || head[0] != 0xFF || head[1] != 0xD8 {
		return 0, 0, false
	}
	i := 2
	for i+1 < len(head) {
		if head[i] != 0xFF {
			return 0, 0, false
		}
		// Skip fill bytes (consecutive 0xFF).
		for i < len(head) && head[i] == 0xFF {
			i++
		}
		if i >= len(head) {
			return 0, 0, false
		}
		marker := head[i]
		if marker == 0x00 || (marker >= 0xD0 && marker <= 0xD7) {
			// Standalone markers (entropy-coded data escape, RSTn): no
			// segment payload — resume scanning at the next byte.
			i++
			continue
		}
		if i+2 >= len(head) {
			return 0, 0, false
		}
		segLen := int(binary.BigEndian.Uint16(head[i+1 : i+3]))
		if segLen < 2 {
			return 0, 0, false
		}
		if isJpegDimensionMarker(marker) {
			if i+8 < len(head) {
				h := int(binary.BigEndian.Uint16(head[i+4 : i+6]))
				w := int(binary.BigEndian.Uint16(head[i+6 : i+8]))
				if w > 0 && h > 0 {
					return w, h, true
				}
			}
			return 0, 0, false
		}
		i += 1 + segLen
	}
	return 0, 0, false
}

func isJpegDimensionMarker(m byte) bool {
	switch m {
	case 0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7,
		0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF:
		return true
	}
	return false
}

// gifDimensions: logical screen descriptor — little-endian uint16 at 6..9.
func gifDimensions(head []byte) (int, int, bool) {
	if len(head) < 10 {
		return 0, 0, false
	}
	magic := string(head[:6])
	if magic != "GIF87a" && magic != "GIF89a" {
		return 0, 0, false
	}
	w := int(binary.LittleEndian.Uint16(head[6:8]))
	h := int(binary.LittleEndian.Uint16(head[8:10]))
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// webpDimensions: only the VP8X (extended) chunk carries the canvas size at
// a fixed offset; VP8/VP8L-only files are absent from the probe (the client
// sizes them at byte arrival).
func webpDimensions(head []byte) (int, int, bool) {
	if len(head) < 30 {
		return 0, 0, false
	}
	if string(head[:4]) != "RIFF" || string(head[8:12]) != "WEBP" {
		return 0, 0, false
	}
	if string(head[12:16]) != "VP8X" {
		return 0, 0, false
	}
	w := int(head[24]) | int(head[25])<<8 | int(head[26])<<16
	h := int(head[27]) | int(head[28])<<8 | int(head[29])<<16
	w++
	h++
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// svgDeclaredSize mirrors the iOS SessionSVGSupport.declaredSize semantics
// (same numbers must come out of both sides — the row height is final from
// the hint, so a mismatch would flip the height at byte arrival):
//   - root `<svg …>` tag = first occurrence of "<svg" (case-sensitive) up to
//     the first ">";
//   - width/height attributes: absolute values only (no unit or "px";
//     percentages fall through), value trimmed;
//   - fallback: viewBox="minX minY width height" (space- or comma-separated,
//     empty fields dropped), width/height = fields 2/3;
//   - anything else → not ok (absent from the probe).
func svgDeclaredSize(head []byte) (int, int, bool) {
	source := string(head)
	tag, ok := svgRootTag(source)
	if !ok {
		return 0, 0, false
	}
	if w, okW := svgAttributeNumber(tag, "width"); okW {
		if h, okH := svgAttributeNumber(tag, "height"); okH {
			return int(w), int(h), true
		}
	}
	viewBox, ok := svgAttributeValue(tag, "viewBox")
	if !ok {
		return 0, 0, false
	}
	fields := strings.FieldsFunc(viewBox, func(r rune) bool { return r == ' ' || r == ',' })
	if len(fields) != 4 {
		return 0, 0, false
	}
	w, errW := strconv.ParseFloat(fields[2], 64)
	h, errH := strconv.ParseFloat(fields[3], 64)
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return int(w), int(h), true
}

func svgRootTag(source string) (string, bool) {
	start := strings.Index(source, "<svg")
	if start < 0 {
		return "", false
	}
	rest := source[start:]
	end := strings.IndexByte(rest, '>')
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// svgAttributeValue mirrors the iOS attribute regex
// `(?<![A-Za-z0-9_-])name\s*=\s*["']([^"']*)["']`: the attribute name must
// not be part of a longer name (preceding byte not [A-Za-z0-9_-]), and the
// quoted value must not contain either quote character.
func svgAttributeValue(tag, name string) (string, bool) {
	searchFrom := 0
	for {
		at := strings.Index(tag[searchFrom:], name)
		if at < 0 {
			return "", false
		}
		at += searchFrom
		searchFrom = at + 1
		if at > 0 && isSvgNameByte(tag[at-1]) {
			continue
		}
		rest := tag[at+len(name):]
		k := 0
		for k < len(rest) && isSvgSpaceByte(rest[k]) {
			k++
		}
		if k >= len(rest) || rest[k] != '=' {
			continue
		}
		k++
		for k < len(rest) && isSvgSpaceByte(rest[k]) {
			k++
		}
		if k >= len(rest) {
			continue
		}
		quote := rest[k]
		if quote != '"' && quote != '\'' {
			continue
		}
		k++
		// Value: up to the first quote character of either kind; the closing
		// one must match the opener ([^"']* semantics).
		v := k
		for v < len(rest) && rest[v] != '"' && rest[v] != '\'' {
			v++
		}
		if v >= len(rest) || rest[v] != quote {
			continue
		}
		return rest[k:v], true
	}
}

// svgAttributeNumber: absolute value only — trimmed, optional "px" suffix,
// positive float (percentages and other units fall through to viewBox).
func svgAttributeNumber(tag, name string) (float64, bool) {
	raw, ok := svgAttributeValue(tag, name)
	if !ok {
		return 0, false
	}
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, "px")
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func isSvgNameByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' ||
		c >= '0' && c <= '9' || c == '_' || c == '-'
}

func isSvgSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
