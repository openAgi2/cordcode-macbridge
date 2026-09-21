package opencodeweb

import (
	"context"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// model_resolution_test.go — 2026-09-21 真机事故回归（owner 裁决：兜底蒙混
// 等于掩盖真实问题，用户手选必须要么生效要么响亮失败）：
//
//  1. wire 的 model.id 沿目录约定携带 provider 前缀（"providerID/modelID"），
//     SendWithOptions 不得再与 opts.ProviderID 拼一次——真机：iOS 手选
//     ctyun/deepseek-v4-flash-0731-oc 被拼成 ctyun/ctyun/...，目录必 miss。
//  2. 用户显式手选目录外模型必须 fail loud（镜像上游 prompt.ts:602-609
//     ModelNotFoundError + did-you-mean），绝不静默兜底成第一个 connected
//     provider 的默认——真机后果是静默跑成 zhipuai Highspeed 并报订阅权限错，
//     用户完全看不到「选的模型根本不存在」的真相。

// providerCatalogFixture mirrors the incident serve shape: zhipuai-coding-plan
// is the FIRST connected provider with default glm-5.3-highspeed; ctyun
// carries deepseek-v4-flash-0731-oc; deepseek does NOT have that id.
const providerCatalogFixture = `{
  "all": [
    {"id":"zhipuai-coding-plan","name":"Zhipu","source":"api","models":{
      "glm-5.3-highspeed":{"id":"glm-5.3-highspeed","name":"GLM-5.3-Highspeed"},
      "glm-5.3-flash":{"id":"glm-5.3-flash","name":"GLM-5.3-Flash"}}},
    {"id":"deepseek","name":"DeepSeek","source":"api","models":{
      "deepseek-v4-flash":{"id":"deepseek-v4-flash","name":"DeepSeek V4 Flash"}}},
    {"id":"ctyun","name":"CTYun","source":"api","models":{
      "deepseek-v4-flash-0731-oc":{"id":"deepseek-v4-flash-0731-oc","name":"DeepSeek V4 Flash 0731 OC"}}},
    {"id":"not-connected","name":"X","source":"api","models":{
      "ghost":{"id":"ghost","name":"Ghost"}}}
  ],
  "default": {"zhipuai-coding-plan":"glm-5.3-highspeed"},
  "connected": ["zhipuai-coding-plan","deepseek","ctyun"]
}`

const agentRegistryFixture = `[
  {"name":"build","mode":"primary","description":"build","hidden":false,"native":false,"model":""}
]`

func newResolutionTestSession(t *testing.T) (*Agent, *serverSession) {
	t.Helper()
	base := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses: map[string]string{
			"/global/health": `{"healthy":true}`,
			"/session":       `[]`,
			"/provider":      providerCatalogFixture,
			"/agent":         agentRegistryFixture,
			"/config":        `{}`,
		},
	})
	a, err := New(map[string]any{
		"work_dir":         "/tmp/p",
		"opencode_web_url": base,
		"opencode_web_user": "u",
		"opencode_web_pass": "p",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	t.Cleanup(func() { _ = agent.Stop() })
	c, err := agent.clientFor(context.Background())
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	return agent, &serverSession{a: agent, client: c, ctx: context.Background()}
}

func TestNormalizePromptModelRef(t *testing.T) {
	// wire id 自带前缀：前缀权威，providerId 不得再拼一次（事故形态）。
	ref := normalizePromptModelRef("ctyun", "ctyun/deepseek-v4-flash-0731-oc")
	if ref.ProviderID != "ctyun" || ref.ID != "deepseek-v4-flash-0731-oc" {
		t.Fatalf("qualified id must split once, got %s/%s", ref.ProviderID, ref.ID)
	}
	// 裸 id：沿用独立 providerId。
	ref = normalizePromptModelRef("zhipuai-coding-plan", "glm-5.3-flash")
	if ref.ProviderID != "zhipuai-coding-plan" || ref.ID != "glm-5.3-flash" {
		t.Fatalf("bare id keeps providerId, got %s/%s", ref.ProviderID, ref.ID)
	}
	// 空选择保持空（默认链入口）。
	ref = normalizePromptModelRef("", "")
	if ref.ProviderID != "" || ref.ID != "" {
		t.Fatalf("empty selection must stay empty, got %s/%s", ref.ProviderID, ref.ID)
	}
}

func TestResolvePromptModelUserPickOutsideCatalogFailsLoud(t *testing.T) {
	_, s := newResolutionTestSession(t)
	// 事故语义形态：模型 ID 真实存在但挂在不同 provider 下——手选无效时
	// 必须拒绝发送并给出 did-you-mean，而不是静默跑成 zhipuai 默认。
	_, err := s.resolvePromptModel(context.Background(), s.client,
		ocwModelRef{ProviderID: "deepseek", ID: "deepseek-v4-flash-0731-oc"}, "")
	if err == nil {
		t.Fatal("user pick outside the connected catalog must fail loudly")
	}
	for _, frag := range []string{"model not found in connected catalog", "nothing was run", "ctyun/deepseek-v4-flash-0731-oc"} {
		if !strings.Contains(err.Error(), frag) {
			t.Fatalf("refusal must carry %q, got %v", frag, err)
		}
	}
}

func TestResolvePromptModelStalePendingPickFailsLoud(t *testing.T) {
	agent, s := newResolutionTestSession(t)
	agent.SetModel("gone/ghost-model") // 目录已不存在的旧手选
	if _, err := s.resolvePromptModel(context.Background(), s.client, ocwModelRef{}, ""); err == nil || !strings.Contains(err.Error(), "model not found in connected catalog") {
		t.Fatalf("stale pending pick must refuse the send, got %v", err)
	}
}

func TestResolvePromptModelNoPickKeepsDefaultChain(t *testing.T) {
	_, s := newResolutionTestSession(t)
	// 没有手选：文档化默认链照旧（第一个 connected provider 的默认）。
	ref, err := s.resolvePromptModel(context.Background(), s.client, ocwModelRef{}, "")
	if err != nil {
		t.Fatalf("default chain: %v", err)
	}
	if ref.ProviderID != "zhipuai-coding-plan" || ref.ID != "glm-5.3-highspeed" {
		t.Fatalf("default chain must resolve the first connected provider default, got %s/%s", ref.ProviderID, ref.ID)
	}
}

func TestResolvePromptModelStaleAgentModelAdvancesToDefaultChain(t *testing.T) {
	_, s := newResolutionTestSession(t)
	// agent 配置模型失效属于配置回退（非用户手选）：继续默认链，不 fail loud。
	ref, err := s.resolvePromptModel(context.Background(), s.client, ocwModelRef{}, "gone/ghost-model")
	if err != nil {
		t.Fatalf("stale agent model must advance to the default chain, got %v", err)
	}
	if ref.ID != "glm-5.3-highspeed" {
		t.Fatalf("advanced resolution must be the provider default, got %s/%s", ref.ProviderID, ref.ID)
	}
}

func TestSendWithOptionsQualifiedIDResolvesWithoutDoublePrefix(t *testing.T) {
	_, s := newResolutionTestSession(t)
	s.alive.Store(true)
	// iOS 真实 wire 形态：id 已含前缀 + providerId 并存。归一化后模型解析
	// 必须成功（后续才会因 fake 缺 session 路由失败——而不是 model not found）。
	err := s.SendWithOptions("probe", nil, nil, core.PromptOptions{
		ProviderID: "ctyun",
		ModelID:    "ctyun/deepseek-v4-flash-0731-oc",
	})
	if err != nil && strings.Contains(err.Error(), "model not found") {
		t.Fatalf("qualified wire id was double-prefixed: %v", err)
	}
}
