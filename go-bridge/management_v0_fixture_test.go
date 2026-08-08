package gobridge

// Management v0 observed fixture proof (plan A-1.1 / R11 P2-3).
//
// v0-status-observed.json 必须由真实 Go handleStatus 在确定性、非敏感测试配置 + 注入固定时钟下
// 产出，且 producer round-trip 逐 byte 相等。本文件就是那个 producer：
//   - 构造 ManagementServer（固定非敏感 BridgeID/DisplayName/Token/Agents）；
//   - 注入固定 startedAt + 固定 now（s.now），使 uptime 成为确定性字符串；
//   - 经 httptest 调用真实 ServeHTTP → handleStatus → writeMgmtJSON（encoding/json）；
//   - 捕获完整 bytes。
//
// 该 fixture 是 Management root 唯一当前 evidenceStatus=observed 的样本；其余 v1 变体为 proposed，
// 等 R1.11 真实 handler 落地后才转 observed。本文件不写本机 bridge ID / 真实 display name / 真实 token。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// v0FixtureConfig 是固定、非敏感、确定性的测试配置。任何字段都不得是本机真实值。
func v0FixtureConfig() ManagementConfig {
	return ManagementConfig{
		Handlers:     NewHandlers(),
		// Token 只用于通过 checkAuth；handleStatus 的 v0 响应不输出 token，因此该值不影响 fixture bytes。
		// 复用既有 testMgmtToken 以与 authRequest helper 一致。
		Token:        testMgmtToken,
		PairingStore: NewMemoryPairingStore(),
		DeviceStore:  NewMemoryDeviceStore(),
		BridgeID:     "cordcode-fixture-bridge",
		DisplayName:  "CordCode Fixture",
		// Agents 非空 -> handleStatus 输出 status:"ready"（与 R11 live dump 一致）。
		Agents: map[string]core.Agent{
			"claude": &mgmtFakeAgent{name: "claudecode"},
		},
	}
}

// v0FixtureClock 返回 (startedAt, now)，使 uptime 恰为 "1h0m0s"（确定性）。
// 选用固定 Unix 纪元瞬间，避免依赖测试执行时间。
func v0FixtureClock() (time.Time, func() time.Time) {
	startedAt := time.Unix(1750000000, 0).UTC() // 2026-06-15 ~ 固定瞬间，仅为可复现
	now := startedAt.Add(1 * time.Hour)
	return startedAt, func() time.Time { return now }
}

// newV0FixtureServer 构造注入了固定时钟的 ManagementServer（直接复用生产 NewManagementServer）。
func newV0FixtureServer() *ManagementServer {
	s := NewManagementServer(v0FixtureConfig())
	startedAt, now := v0FixtureClock()
	s.startedAt = startedAt
	s.now = now
	return s
}

// generateV0StatusBytes 调用真实 handleStatus 并返回完整 HTTP body bytes。
func generateV0StatusBytes(t *testing.T) []byte {
	t.Helper()
	srv := newV0FixtureServer()
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/status")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleStatus status=%d, want 200", rec.Code)
	}
	return rec.Body.Bytes()
}

// fixtureV0Path 指向已提交的 observed fixture（相对 go-bridge 包目录）。
func fixtureV0Path() string {
	return filepath.Join("..", "docs", "protocol", "samples", "management-file-read", "v0-status-observed.json")
}

// TestManagementV0Status_ProducerShape 断言真实 producer bytes 解码后恰好是 R11 观察到的
// 5 个 string 字段、status==ready，且没有 iosPort（避免把 Mac Swift 模型 optional 误当 producer 事实）。
func TestManagementV0Status_ProducerShape(t *testing.T) {
	raw := generateV0StatusBytes(t)
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("producer bytes 不是合法 JSON: %v\nraw=%s", err, raw)
	}
	wantKeys := []string{"bridgeId", "displayName", "status", "uptime", "version"}
	if len(body) != len(wantKeys) {
		t.Fatalf("producer 字段数=%d, want %d (keys=%v)", len(body), len(wantKeys), body)
	}
	for _, k := range wantKeys {
		v, ok := body[k]
		if !ok {
			t.Errorf("producer 缺字段 %q", k)
			continue
		}
		// 每个值必须是 JSON string token。
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			t.Errorf("producer 字段 %q 不是 string: %v (raw=%s)", k, err, v)
		}
	}
	if _, ok := body["iosPort"]; ok {
		t.Errorf("producer 不应输出 iosPort（仅 Mac Swift 模型 optional）")
	}
	var typed map[string]string
	if err := json.Unmarshal(raw, &typed); err != nil {
		t.Fatalf("re-decode as map[string]string 失败: %v", err)
	}
	if typed["status"] != "ready" {
		t.Errorf("status=%q, want ready", typed["status"])
	}
	if typed["bridgeId"] != "cordcode-fixture-bridge" {
		t.Errorf("bridgeId=%q, want cordcode-fixture-bridge", typed["bridgeId"])
	}
	if typed["uptime"] != "1h0m0s" {
		t.Errorf("uptime=%q, want 1h0m0s（固定时钟确定性）", typed["uptime"])
	}
}

// TestManagementV0Status_ObservedRoundTrip 是 A-1.1 通过条件“producer round-trip 逐 byte 相等”：
// 重新用同一确定性输入跑真实 handleStatus，与已提交 observed fixture 逐 byte 比较。
func TestManagementV0Status_ObservedRoundTrip(t *testing.T) {
	raw := generateV0StatusBytes(t)
	golden, err := os.ReadFile(fixtureV0Path())
	if err != nil {
		t.Fatalf("读取已提交 observed fixture 失败 (%s): %v\n请先运行: CCCODEGEN_FIXTURES=1 go test ./go-bridge/ -run TestManagementV0Status_GenerateFixture -count=1", fixtureV0Path(), err)
	}
	if string(raw) != string(golden) {
		t.Fatalf("producer round-trip 不一致:\n--- generated (%d bytes) ---\n%s\n--- committed (%d bytes) ---\n%s", len(raw), raw, len(golden), golden)
	}
}

// TestManagementV0Status_GenerateFixture 是 generator：在 CCCODEGEN_FIXTURES=1 时把真实 producer bytes
// 写入已提交 fixture 路径，使 round-trip 测试有稳定 golden 可比。日常 CI 不写文件。
func TestManagementV0Status_GenerateFixture(t *testing.T) {
	if os.Getenv("CCCODEGEN_FIXTURES") != "1" {
		t.Skip("set CCCODEGEN_FIXTURES=1 to (re)write the committed v0 observed fixture")
	}
	raw := generateV0StatusBytes(t)
	if err := os.MkdirAll(filepath.Dir(fixtureV0Path()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(fixtureV0Path(), raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Logf("wrote %d bytes to %s", len(raw), fixtureV0Path())
}
