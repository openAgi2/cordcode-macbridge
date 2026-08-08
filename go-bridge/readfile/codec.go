// Package readfile 实现 CordCode 文件读取 wire（legacy read_file + read_file_v2 + cancel/chunk）
// 的 strict codec，作为 plan A-1 的 Bridge/Relay wire root proof artifact。
//
// 当前仅被定向测试引用（go-bridge/readfile/*_test.go），未被任何 handler 接线，因此不影响
// 运行期二进制行为；R1.1 才把它接入真实 handler。规范来源：
// docs(cordcode-ios)/2026-08-08-syntax-highlighting-shiki-jsc-plan.md §3.6.1/§3.6.2 + R11。
//
// 为避免与 go-bridge/admission 产生语义耦合，本包自带 strict 原语（R1 可抽取共享 wirestrict 包）。
package readfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A0.5 named caps（spec-derived；性能相关项 pending A3a/R1）。
const (
	MaxReadFileSerializedBytes uint64 = 4 * 1024 * 1024 // 4 MiB（>2MiB source + JSON 开销）
	MaxPathUTF8Bytes           uint64 = 32 * 1024
	MaxSegments                uint64 = 8
	MaxTotalLines              uint64 = 5000
	MaxSourceLineCount         uint64 = 5000
)

// sha256RevRegex: contentRevision = sha256:<64 lowercase hex>。
var sha256RevRegex = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// correlationIDRegex: bulkCorrelationId = 16 bytes as 32 lowercase hex（非 secret；不要求 constant-time）。
var correlationIDRegex = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ── strict 原语（与 admission 包同构；R1 可抽取共享包）────────────────────────────

func parseStrictUInt(raw json.RawMessage, bits int) (uint64, error) {
	s := strings.TrimSpace(string(raw))
	if len(s) == 0 {
		return 0, errMissingNumber
	}
	switch c := s[0]; {
	case c == '"':
		return 0, errQuotedNumber
	case c == 'n':
		return 0, errNullNumber
	case c == 't' || c == 'f':
		return 0, errBoolNumber
	case c == '-' || c == '+' || c == '.':
		return 0, errInvalidNumberToken
	case c >= '0' && c <= '9':
	default:
		return 0, errInvalidNumberToken
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errInvalidNumberToken
		}
	}
	v, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, errNumberOverflow
	}
	return v, nil
}

func assertNoDuplicateKeys(raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return errInvalidJSON
	}
	delim, ok := t.(json.Delim)
	if !ok || delim != '{' {
		return errInvalidJSON
	}
	seen := make(map[string]bool)
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return errInvalidJSON
		}
		key, ok := kt.(string)
		if !ok {
			return errInvalidJSON
		}
		if seen[key] {
			return fmt.Errorf("%w: %s", errDuplicateKey, key)
		}
		seen[key] = true
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return errInvalidJSON
		}
	}
	return nil
}

// decodeStrictObject 校验 exact key set（allowed 列表）、无 duplicate、无 null。
func decodeStrictObject(raw json.RawMessage, allowed []string) (map[string]json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errInvalidJSON
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errInvalidJSON
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowedSet[k] = true
	}
	for k := range m {
		if !allowedSet[k] {
			return nil, fmt.Errorf("%w: %s", errUnknownField, k)
		}
	}
	for _, k := range allowed {
		if _, ok := m[k]; !ok {
			// detected 字段允许 omit（unsupported_encoding detected unknown 时省略）
			if k == "detected" {
				continue
			}
			return nil, fmt.Errorf("%w: %s", errMissingField, k)
		}
	}
	for k, v := range m {
		if strings.TrimSpace(string(v)) == "null" {
			return nil, fmt.Errorf("%w: %s", errNullField, k)
		}
	}
	if err := assertNoDuplicateKeys(raw); err != nil {
		return nil, err
	}
	return m, nil
}

func decodeStrictString(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 {
		return "", errMissingField
	}
	if trimmed == "null" {
		return "", errNullField
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errInvalidString
	}
	return s, nil
}

// ── types ───────────────────────────────────────────────────────────────────────

type OwningIdentity struct {
	Kind                  string // "session" | "workspace"
	BackendID             string
	SessionID             string // session only
	CanonicalDirectory    string // session only
	CanonicalWorkspaceRoot string // workspace only
}

type FileMetadata struct {
	Path            string
	Extension       string // 始终为 string，无扩展名时 ""
	SizeBytes       uint64
	ContentRevision string // sha256:<64hex>
	OwningIdentity  OwningIdentity
}

type Segment struct {
	Kind            string // full | head | omission | tail
	Content         string   // full/head/tail only
	SourceLineStart uint64
	SourceLineCount uint64 // full/head/tail: sourceLineCount; omission: omittedLineCount
}

type ReadFileV2Result struct {
	Kind      string // text | unsupported_encoding | binary
	Metadata  FileMetadata
	Encoding  string   // text only ("utf-8")
	TotalLines uint64  // text only
	Detected  string   // unsupported_encoding only; "" when omitted
	Segments  []Segment // text only
}

// ── decoders ────────────────────────────────────────────────────────────────────

// DecodeLegacyReadFileResult strict-decode 当前 6 字段 wire（handlers.go:3823）。
func DecodeLegacyReadFileResult(raw json.RawMessage) (map[string]json.RawMessage, error) {
	return decodeStrictObject(raw, []string{"path", "content", "extension", "sizeBytes", "totalLines", "truncated"})
}

// DecodeReadFileV2Request strict-decode path + owner tagged（session|workspace）。
func DecodeReadFileV2Request(raw json.RawMessage) (path string, owner OwningIdentity, err error) {
	m, err := decodeStrictObject(raw, []string{"path", "owner"})
	if err != nil {
		return "", OwningIdentity{}, err
	}
	path, err = decodeStrictString(m["path"])
	if err != nil {
		return "", OwningIdentity{}, err
	}
	if uint64(len(path)) > MaxPathUTF8Bytes {
		return "", OwningIdentity{}, errPathTooLong
	}
	// 先 loose 读 owner.kind，再用对应 variant 的完整 key 集 strict 解码。
	var ownerLoose map[string]json.RawMessage
	if err := json.Unmarshal(m["owner"], &ownerLoose); err != nil {
		return "", OwningIdentity{}, errInvalidJSON
	}
	kindRaw, ok := ownerLoose["kind"]
	if !ok {
		return "", OwningIdentity{}, fmt.Errorf("%w: owner.kind", errMissingField)
	}
	kind, err := decodeStrictString(kindRaw)
	if err != nil {
		return "", OwningIdentity{}, err
	}
	backend := ""
	owner = OwningIdentity{Kind: kind}
	switch kind {
	case "session":
		om, err := decodeStrictObject(m["owner"], []string{"kind", "backendId", "sessionId", "directory"})
		if err != nil {
			return "", OwningIdentity{}, err
		}
		backend, err = decodeStrictString(om["backendId"])
		if err != nil {
			return "", OwningIdentity{}, err
		}
		owner.SessionID, err = decodeStrictString(om["sessionId"])
		if err != nil {
			return "", OwningIdentity{}, err
		}
		owner.CanonicalDirectory, err = decodeStrictString(om["directory"])
		if err != nil {
			return "", OwningIdentity{}, err
		}
	case "workspace":
		om, err := decodeStrictObject(m["owner"], []string{"kind", "backendId", "workspaceRoot"})
		if err != nil {
			return "", OwningIdentity{}, err
		}
		backend, err = decodeStrictString(om["backendId"])
		if err != nil {
			return "", OwningIdentity{}, err
		}
		owner.CanonicalWorkspaceRoot, err = decodeStrictString(om["workspaceRoot"])
		if err != nil {
			return "", OwningIdentity{}, err
		}
	default:
		return "", OwningIdentity{}, fmt.Errorf("%w: owner.kind=%s", errInvalidString, kind)
	}
	owner.BackendID = backend
	return path, owner, nil
}

// DecodeReadFileV2Result strict-decode tagged union（text/unsupported_encoding/binary）。
func DecodeReadFileV2Result(raw json.RawMessage) (ReadFileV2Result, error) {
	// 先 loose 读 kind 决定 allowed keys
	var loose map[string]json.RawMessage
	if err := json.Unmarshal(raw, &loose); err != nil {
		return ReadFileV2Result{}, errInvalidJSON
	}
	kindRaw, ok := loose["kind"]
	if !ok {
		return ReadFileV2Result{}, fmt.Errorf("%w: kind", errMissingField)
	}
	kind, err := decodeStrictString(kindRaw)
	if err != nil {
		return ReadFileV2Result{}, err
	}
	res := ReadFileV2Result{Kind: kind}
	switch kind {
	case "text":
		m, err := decodeStrictObject(raw, []string{"kind", "metadata", "encoding", "totalLines", "segments"})
		if err != nil {
			return res, err
		}
		res.Metadata, err = decodeMetadata(m["metadata"])
		if err != nil {
			return res, err
		}
		res.Encoding, err = decodeStrictString(m["encoding"])
		if err != nil {
			return res, err
		}
		if res.Encoding != "utf-8" {
			return res, fmt.Errorf("%w: encoding=%s (text must be utf-8)", errInvalidString, res.Encoding)
		}
		tl, err := parseStrictUInt(m["totalLines"], 64)
		if err != nil {
			return res, err
		}
		if tl > MaxTotalLines {
			return res, fmt.Errorf("%w: totalLines=%d", errNumberOverflow, tl)
		}
		res.TotalLines = tl
		res.Segments, err = decodeSegments(m["segments"], tl)
		if err != nil {
			return res, err
		}
	case "unsupported_encoding":
		// detected 可 omit；无 encoding/totalLines/segments
		m, err := decodeStrictObject(raw, []string{"kind", "metadata", "detected"})
		if err != nil {
			return res, err
		}
		res.Metadata, err = decodeMetadata(m["metadata"])
		if err != nil {
			return res, err
		}
		if v, ok := m["detected"]; ok {
			res.Detected, err = decodeStrictString(v)
			if err != nil {
				return res, err
			}
		}
	case "binary":
		m, err := decodeStrictObject(raw, []string{"kind", "metadata"})
		if err != nil {
			return res, err
		}
		res.Metadata, err = decodeMetadata(m["metadata"])
		if err != nil {
			return res, err
		}
	default:
		return res, fmt.Errorf("%w: kind=%s", errInvalidString, kind)
	}
	return res, nil
}

func decodeMetadata(raw json.RawMessage) (FileMetadata, error) {
	m, err := decodeStrictObject(raw, []string{"path", "extension", "sizeBytes", "contentRevision", "owningIdentity"})
	if err != nil {
		return FileMetadata{}, err
	}
	path, err := decodeStrictString(m["path"])
	if err != nil {
		return FileMetadata{}, err
	}
	if uint64(len(path)) > MaxPathUTF8Bytes {
		return FileMetadata{}, errPathTooLong
	}
	ext, err := decodeStrictString(m["extension"])
	if err != nil {
		return FileMetadata{}, err
	}
	size, err := parseStrictUInt(m["sizeBytes"], 64)
	if err != nil {
		return FileMetadata{}, err
	}
	if size > MaxReadFileSerializedBytes {
		return FileMetadata{}, fmt.Errorf("%w: sizeBytes=%d", errNumberOverflow, size)
	}
	rev, err := decodeStrictString(m["contentRevision"])
	if err != nil {
		return FileMetadata{}, err
	}
	if !sha256RevRegex.MatchString(rev) {
		return FileMetadata{}, errBadRevision
	}
	own, err := decodeOwningIdentity(m["owningIdentity"])
	if err != nil {
		return FileMetadata{}, err
	}
	return FileMetadata{Path: path, Extension: ext, SizeBytes: size, ContentRevision: rev, OwningIdentity: own}, nil
}

func decodeOwningIdentity(raw json.RawMessage) (OwningIdentity, error) {
	// owningIdentity 是 server-canonical 回显：session/workspace tagged
	var loose map[string]json.RawMessage
	if err := json.Unmarshal(raw, &loose); err != nil {
		return OwningIdentity{}, errInvalidJSON
	}
	kindRaw, ok := loose["kind"]
	if !ok {
		return OwningIdentity{}, fmt.Errorf("%w: owningIdentity.kind", errMissingField)
	}
	kind, err := decodeStrictString(kindRaw)
	if err != nil {
		return OwningIdentity{}, err
	}
	switch kind {
	case "session":
		m, err := decodeStrictObject(raw, []string{"kind", "backendId", "sessionId", "canonicalDirectory"})
		if err != nil {
			return OwningIdentity{}, err
		}
		be, _ := decodeStrictString(m["backendId"])
		sid, _ := decodeStrictString(m["sessionId"])
		dir, _ := decodeStrictString(m["canonicalDirectory"])
		return OwningIdentity{Kind: "session", BackendID: be, SessionID: sid, CanonicalDirectory: dir}, nil
	case "workspace":
		m, err := decodeStrictObject(raw, []string{"kind", "backendId", "canonicalWorkspaceRoot"})
		if err != nil {
			return OwningIdentity{}, err
		}
		be, _ := decodeStrictString(m["backendId"])
		root, _ := decodeStrictString(m["canonicalWorkspaceRoot"])
		return OwningIdentity{Kind: "workspace", BackendID: be, CanonicalWorkspaceRoot: root}, nil
	default:
		return OwningIdentity{}, fmt.Errorf("%w: owningIdentity.kind=%s", errInvalidString, kind)
	}
}

func decodeSegments(raw json.RawMessage, totalLines uint64) ([]Segment, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, errInvalidJSON
	}
	if uint64(len(arr)) > MaxSegments {
		return nil, fmt.Errorf("%w: segments=%d", errNumberOverflow, len(arr))
	}
	if len(arr) == 0 {
		return nil, errEmptySegments // text 必须有至少一个 full segment（空文件也带空 full）
	}
	out := make([]Segment, 0, len(arr))
	var lineSum uint64
	for _, sr := range arr {
		var loose map[string]json.RawMessage
		if err := json.Unmarshal(sr, &loose); err != nil {
			return nil, errInvalidJSON
		}
		kindRaw, ok := loose["kind"]
		if !ok {
			return nil, fmt.Errorf("%w: segment.kind", errMissingField)
		}
		kind, err := decodeStrictString(kindRaw)
		if err != nil {
			return nil, err
		}
		seg := Segment{Kind: kind}
		switch kind {
		case "full", "head", "tail":
			m, err := decodeStrictObject(sr, []string{"kind", "content", "sourceLineStart", "sourceLineCount"})
			if err != nil {
				return nil, err
			}
			seg.Content, _ = decodeStrictString(m["content"])
			seg.SourceLineStart, err = parseStrictUInt(m["sourceLineStart"], 64)
			if err != nil {
				return nil, err
			}
			lc, err := parseStrictUInt(m["sourceLineCount"], 64)
			if err != nil {
				return nil, err
			}
			seg.SourceLineCount = lc
			lineSum += lc
		case "omission":
			m, err := decodeStrictObject(sr, []string{"kind", "sourceLineStart", "omittedLineCount"})
			if err != nil {
				return nil, err
			}
			seg.SourceLineStart, err = parseStrictUInt(m["sourceLineStart"], 64)
			if err != nil {
				return nil, err
			}
			lc, err := parseStrictUInt(m["omittedLineCount"], 64)
			if err != nil {
				return nil, err
			}
			seg.SourceLineCount = lc // 复用字段存 omittedLineCount
			lineSum += lc
		default:
			return nil, fmt.Errorf("%w: segment.kind=%s", errInvalidString, kind)
		}
		out = append(out, seg)
	}
	if lineSum != totalLines {
		return nil, fmt.Errorf("%w: segment line sum=%d != totalLines=%d", errSegmentLineMismatch, lineSum, totalLines)
	}
	return out, nil
}

// DecodeRelayChunk strict-decode chunk metadata（base 或 correlated）。
func DecodeRelayChunk(raw json.RawMessage) (hasCorrelation bool, err error) {
	var loose map[string]json.RawMessage
	if err := json.Unmarshal(raw, &loose); err != nil {
		return false, errInvalidJSON
	}
	if _, ok := loose["bulkCorrelationId"]; ok {
		m, err := decodeStrictObject(raw, []string{"groupId", "index", "count", "bulkCorrelationId"})
		if err != nil {
			return false, err
		}
		cid, err := decodeStrictString(m["bulkCorrelationId"])
		if err != nil {
			return false, err
		}
		if !correlationIDRegex.MatchString(cid) {
			return false, errBadCorrelation
		}
		return true, nil
	}
	_, err = decodeStrictObject(raw, []string{"groupId", "index", "count"})
	return false, err
}

// ── errors ──────────────────────────────────────────────────────────────────────

var (
	errMissingNumber       = errors.New("missing numeric field")
	errQuotedNumber        = errors.New("number field must not be quoted")
	errNullNumber          = errors.New("number field must not be null")
	errBoolNumber          = errors.New("number field must not be a bool")
	errInvalidNumberToken  = errors.New("number field must be a base-10 non-negative integer token")
	errNumberOverflow      = errors.New("numeric field overflow / over cap")
	errInvalidJSON         = errors.New("invalid JSON object")
	errUnknownField        = errors.New("unknown field")
	errMissingField        = errors.New("missing required field")
	errNullField           = errors.New("field must not be null")
	errDuplicateKey        = errors.New("duplicate key")
	errInvalidString       = errors.New("invalid string field")
	errBadRevision         = errors.New("contentRevision must be sha256:<64 lowercase hex>")
	errBadCorrelation      = errors.New("bulkCorrelationId must be 32 lowercase hex")
	errPathTooLong         = errors.New("path exceeds MaxPathUTF8Bytes")
	errEmptySegments       = errors.New("text result must have at least one full segment")
	errSegmentLineMismatch = errors.New("segment line count sum != totalLines")
)
