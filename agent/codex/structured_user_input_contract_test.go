package codex

// structured_user_input_contract_test.go 锁定 Codex app-server 结构化用户输入的外部 wire 格式。
// 所有断言直接读取 testdata/structured_user_input/ 下已归档的 generated schema
// (codex-cli 0.146.0-alpha.9.2)，不依赖 adapter 代码，也不得反向自造 fixture。
// 依据：docs/2026-08-01-codex-claude-structured-user-input-design.md §3.2 / §8 / §14 P0。
//
// 这些 contract test 是 P0 的格式冻结门：若 Codex 升级导致 schema 漂移，本文件必须先红，
// 再由设计阶段重新取证，而不是由开发 agent 在 P1 现场猜测格式。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

const suiFixturesDir = "testdata/structured_user_input"

func loadSUISchema(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(suiFixturesDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	return m
}

// oneOfMethodVariants 遍历 schema.oneOf[].properties.method.enum，返回 method->index。
// 强制走 oneOf 遍历（而不是顶层 method enum）正是 §19 评审护栏：避免读者假设根 enum。
func oneOfMethodVariants(t *testing.T, schema map[string]any) map[string]int {
	t.Helper()
	oneOf, ok := schema["oneOf"].([]any)
	if !ok || len(oneOf) == 0 {
		t.Fatalf("schema 缺少 oneOf 数组")
	}
	out := map[string]int{}
	for i, v := range oneOf {
		variant, _ := v.(map[string]any)
		props, _ := variant["properties"].(map[string]any)
		method, _ := props["method"].(map[string]any)
		enum, _ := method["enum"].([]any)
		for _, e := range enum {
			if s, ok := e.(string); ok {
				out[s] = i
			}
		}
	}
	return out
}

func suiResolveDef(schema map[string]any, ref string) map[string]any {
	defs, _ := schema["definitions"].(map[string]any)
	name := filepath.Base(ref)
	d, _ := defs[name].(map[string]any)
	return d
}

func suiRef(schema map[string]any, parent map[string]any, key string) map[string]any {
	v, _ := parent[key].(map[string]any)
	if ref, ok := v["$ref"].(string); ok {
		return suiResolveDef(schema, ref)
	}
	return v
}

func suiHasType(m map[string]any, want string) bool {
	switch tt := m["type"].(type) {
	case string:
		return tt == want
	case []any:
		for _, x := range tt {
			if s, ok := x.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// suiIsRequestId 判断某字段定义是否等价于 RequestId（string | int64）。
// 归档 schema 中 RequestId 可能以 $ref、顶层 anyOf 或内联 anyOf 出现。
func suiIsRequestId(def map[string]any) bool {
	if suiHasType(def, "string") || suiHasType(def, "integer") {
		// 单 type 不足以同时表达 string|int64，但作为宽松匹配保留。
	}
	if anyOf, ok := def["anyOf"].([]any); ok {
		hasStr, hasInt := false, false
		for _, v := range anyOf {
			m, _ := v.(map[string]any)
			if suiHasType(m, "string") {
				hasStr = true
			}
			if suiHasType(m, "integer") {
				hasInt = true
			}
		}
		return hasStr && hasInt
	}
	return false
}

// --- ServerRequest: item/tool/requestUserInput 必须经 oneOf 遍历命中 ---

func TestCodexServerRequestUserInputFoundViaOneOfTraversal(t *testing.T) {
	schema := loadSUISchema(t, "ServerRequest.json")
	// 护栏：根 properties 若给出 method enum，会让读者跳过 oneOf；归档 schema 不应如此。
	if props, ok := schema["properties"].(map[string]any); ok {
		if m, ok := props["method"].(map[string]any); ok {
			if _, hasEnum := m["enum"]; hasEnum {
				t.Fatalf("ServerRequest 根存在 method enum：contract 必须遍历 oneOf，不能读根 enum")
			}
		}
	}
	variants := oneOfMethodVariants(t, schema)
	if _, ok := variants["item/tool/requestUserInput"]; !ok {
		got := make([]string, 0, len(variants))
		for k := range variants {
			got = append(got, k)
		}
		sort.Strings(got)
		t.Fatalf("未在 oneOf 中找到 item/tool/requestUserInput；现有 methods=%v", got)
	}
	if len(variants) < 8 {
		t.Fatalf("ServerRequest oneOf 变体数 %d 过少，疑似 schema 退化", len(variants))
	}
}

// --- request id: string | int64 ---

func TestCodexRequestIdIsStringOrInt64(t *testing.T) {
	schema := loadSUISchema(t, "RequestId.json")
	anyOf, ok := schema["anyOf"].([]any)
	if !ok || len(anyOf) != 2 {
		t.Fatalf("RequestId 应为 anyOf[string,int64]，实际 anyOf=%v", schema["anyOf"])
	}
	seen := map[string]bool{}
	for _, v := range anyOf {
		m, _ := v.(map[string]any)
		if suiHasType(m, "string") {
			seen["string"] = true
		}
		if suiHasType(m, "integer") {
			if fmt, _ := m["format"].(string); fmt == "int64" {
				seen["int64"] = true
			}
		}
	}
	if !seen["string"] || !seen["int64"] {
		t.Fatalf("RequestId 必须是 string|int64，得到 %v", seen)
	}
}

// --- requestUserInput params shape (threadId/turnId/itemId/questions + autoResolutionMs?) ---

func TestCodexRequestUserInputParamsShape(t *testing.T) {
	schema := loadSUISchema(t, "ToolRequestUserInputParams.json")
	required, _ := schema["required"].([]any)
	wantReq := map[string]bool{"itemId": true, "questions": true, "threadId": true, "turnId": true}
	for _, r := range required {
		delete(wantReq, r.(string))
	}
	if len(wantReq) != 0 {
		t.Fatalf("params required 缺少字段 %v；实际 required=%v", wantReq, required)
	}
	props, _ := schema["properties"].(map[string]any)

	// autoResolutionMs: integer(uint64) | null，且非 required（可选）。
	if arm, ok := props["autoResolutionMs"].(map[string]any); ok {
		if !suiHasType(arm, "integer") || !suiHasType(arm, "null") {
			t.Fatalf("autoResolutionMs 应为 integer|null，实际 type=%v", arm["type"])
		}
		if f, _ := arm["format"].(string); f != "uint64" {
			t.Fatalf("autoResolutionMs format 应为 uint64，实际 %q", f)
		}
	}
	for _, req := range required {
		if req == "autoResolutionMs" {
			t.Fatalf("autoResolutionMs 不得是 required（可选字段）")
		}
	}

	// questions -> ToolRequestUserInputQuestion
	questions, _ := props["questions"].(map[string]any)
	qDef := suiResolveDef(schema, questions["items"].(map[string]any)["$ref"].(string))
	qRequired := map[string]bool{"header": true, "id": true, "question": true}
	for _, r := range qDef["required"].([]any) {
		delete(qRequired, r.(string))
	}
	if len(qRequired) != 0 {
		t.Fatalf("question required 缺少 %v；实际=%v", qRequired, qDef["required"])
	}
	qProps, _ := qDef["properties"].(map[string]any)
	for _, opt := range []string{"isOther", "isSecret", "options"} {
		if _, ok := qProps[opt]; !ok {
			t.Fatalf("question 缺少可选字段 %q", opt)
		}
	}
	// options: array|null；option 无 id
	opts, _ := qProps["options"].(map[string]any)
	if !suiHasType(opts, "array") || !suiHasType(opts, "null") {
		t.Fatalf("question.options 应为 array|null，实际 type=%v", opts["type"])
	}
	optDef := suiResolveDef(schema, opts["items"].(map[string]any)["$ref"].(string))
	optReq := map[string]bool{"label": true, "description": true}
	for _, r := range optDef["required"].([]any) {
		delete(optReq, r.(string))
	}
	if len(optReq) != 0 {
		t.Fatalf("option required 应为 [label,description]，缺少 %v", optReq)
	}
	if optProps, _ := optDef["properties"].(map[string]any); optProps != nil {
		if _, hasID := optProps["id"]; hasID {
			t.Fatalf("option 不得带 id 字段（Codex option 无 id，按 index 派生）")
		}
	}
}

// --- response: 每题 answers 恒为 string[] ---

func TestCodexRequestUserInputResponseAnswersIsStringArray(t *testing.T) {
	schema := loadSUISchema(t, "ToolRequestUserInputResponse.json")
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "answers" {
		t.Fatalf("response required 应只含 answers，实际=%v", required)
	}
	props, _ := schema["properties"].(map[string]any)
	answers, _ := props["answers"].(map[string]any)
	answerDef := suiResolveDef(schema, answers["additionalProperties"].(map[string]any)["$ref"].(string))
	aReq, _ := answerDef["required"].([]any)
	if len(aReq) != 1 || aReq[0] != "answers" {
		t.Fatalf("ToolRequestUserInputAnswer required 应为 [answers]，实际=%v", aReq)
	}
	inner, _ := answerDef["properties"].(map[string]any)["answers"].(map[string]any)
	if !suiHasType(inner, "array") {
		t.Fatalf("answer.answers 必须是 array（string[]），实际 type=%v", inner["type"])
	}
	item, _ := inner["items"].(map[string]any)
	if !suiHasType(item, "string") {
		t.Fatalf("answer.answers 元素必须是 string，实际 items=%v", inner["items"])
	}
	// 关键不变量：容器恒为数组，因此 single/text serializer 都必须输出单元素 string[]，
	// 不得输出裸字符串。具体序列化由 P1 adapter test 锁定，这里锁定 schema 不允许裸字符串。
}

// --- serverRequest/resolved notification: {requestId, threadId} ---

func TestCodexServerRequestResolvedNotificationShape(t *testing.T) {
	schema := loadSUISchema(t, "ServerNotification.json")
	variants := oneOfMethodVariants(t, schema)
	idx, ok := variants["serverRequest/resolved"]
	if !ok {
		t.Fatalf("ServerNotification 未找到 serverRequest/resolved")
	}
	variant, _ := schema["oneOf"].([]any)[idx].(map[string]any)
	props, _ := variant["properties"].(map[string]any)
	params := suiResolveDef(schema, props["params"].(map[string]any)["$ref"].(string))
	wantReq := map[string]bool{"requestId": true, "threadId": true}
	for _, r := range params["required"].([]any) {
		delete(wantReq, r.(string))
	}
	if len(wantReq) != 0 {
		t.Fatalf("serverRequest/resolved params required 应为 [requestId,threadId]，缺少 %v", wantReq)
	}
	pprops, _ := params["properties"].(map[string]any)
	rid := suiRef(schema, pprops, "requestId")
	if !suiIsRequestId(rid) {
		t.Fatalf("requestId 应为 RequestId(string|int64)，实际 def=%v", rid)
	}
	if tid, _ := pprops["threadId"].(map[string]any); !suiHasType(tid, "string") {
		t.Fatalf("threadId 应为 string，实际=%v", pprops["threadId"])
	}
}

// --- JSON-RPC response envelope: {id, result} ---

func TestCodexJSONRPCResponseEnvelope(t *testing.T) {
	schema := loadSUISchema(t, "JSONRPCResponse.json")
	required, _ := schema["required"].([]any)
	wantReq := map[string]bool{"id": true, "result": true}
	for _, r := range required {
		delete(wantReq, r.(string))
	}
	if len(wantReq) != 0 {
		t.Fatalf("JSONRPCResponse required 应为 [id,result]，缺少 %v", wantReq)
	}
	props, _ := schema["properties"].(map[string]any)
	idDef := suiResolveDef(schema, props["id"].(map[string]any)["$ref"].(string))
	if !suiIsRequestId(idDef) {
		t.Fatalf("JSONRPCResponse.id 应 $ref RequestId(string|int64)，实际=%v", idDef)
	}
}

// --- reject fail-closed：当前 schema 不含 question/reject / question/reply / turn/question ---

func TestCodexNoLegacyQuestionMethodsInSchema(t *testing.T) {
	for _, name := range []string{"ServerRequest.json", "ServerNotification.json"} {
		schema := loadSUISchema(t, name)
		variants := oneOfMethodVariants(t, schema)
		for _, legacy := range []string{"question/reject", "question/reply", "turn/question"} {
			if _, ok := variants[legacy]; ok {
				t.Fatalf("%s 仍含旧方法 %q：reject 不得走 legacy，必须 fail-closed", name, legacy)
			}
		}
	}
}

// TestCodexManifestCrossCheck 锁定 testdata manifest 与归档 backend_manifest 一致，
// 防止 fixture 被悄悄替换。manifest.json 是 §0 codex_manifest.json 的字节级拷贝。
func TestCodexManifestCrossCheck(t *testing.T) {
	manifest := loadSUISchema(t, "manifest.json")
	files, ok := manifest["files"].(map[string]any)
	if !ok {
		// codex_manifest.json 用 object 形式 {basename: sha256}
		t.Fatalf("codex manifest.files 不是 object: %T", manifest["files"])
	}
	for basename, sha := range files {
		// manifest key 是证据归档名（codex_xxx.json）；testdata 文件名去掉了 codex_ 前缀。
		tdName := basename
		if len(tdName) > 6 && tdName[:6] == "codex_" {
			tdName = tdName[6:]
		}
		data, err := os.ReadFile(filepath.Join(suiFixturesDir, tdName))
		if err != nil {
			t.Fatalf("manifest 引用的文件缺失 %s (%s): %v", basename, tdName, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		want, _ := sha.(string)
		if got != want {
			t.Fatalf("%s SHA-256 不匹配 manifest: got=%s want=%s", basename, got, want)
		}
	}
}
