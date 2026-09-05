package gobridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// concentrated_activation_test.go owns the directive-008 activation-wave
// boundaries: send_message agent/variant survive the bridge boundary, option
// senders never mutate agent-global state, list_models surfaces variants,
// and the Mac canonical protocol pack stays identical to the iOS mirror.

// TestSendMessageAgentVariantSurviveParse: the wire params' agent and
// model.variant decode into PromptOptions verbatim (§6.11.1 items 2/4 — the
// former Swift-boundary drop and the new variant key must both round-trip).
func TestSendMessageAgentVariantSurviveParse(t *testing.T) {
	raw := `{
		"sessionId": "ses_1",
		"content": "hi",
		"agent": "planner",
		"reasoningEffort": "high",
		"model": {"id": "echo", "providerId": "localmock", "variant": "high"}
	}`
	var params SendMessageParams
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if params.Agent != "planner" {
		t.Fatalf("agent = %q (the Swift-boundary drop must be fixed end to end)", params.Agent)
	}
	opts := promptOptionsFromParams(params)
	if opts.Agent != "planner" || opts.ModelID != "echo" || opts.ProviderID != "localmock" || opts.Variant != "high" || opts.ReasoningEffort != "high" {
		t.Fatalf("options = %+v", opts)
	}
	// Absent optional fields stay empty — the backend then resolves itself.
	var bare SendMessageParams
	if err := json.Unmarshal([]byte(`{"sessionId":"s","content":"c"}`), &bare); err != nil {
		t.Fatalf("bare parse: %v", err)
	}
	if got := promptOptionsFromParams(bare); got != (core.PromptOptions{}) {
		t.Fatalf("bare options = %+v, want zero", got)
	}
}

// optionsAgent / optionsSession is a minimal PromptOptionsSender backend.
type optionsAgent struct {
	core.Agent
	gotOpts   core.PromptOptions
	setModel  string
	sendCalls int
}

func (a *optionsAgent) Name() string { return "opt-fixture" }
func (a *optionsAgent) StartSession(ctx context.Context, id string) (core.AgentSession, error) {
	return &optionsSession{agent: a}, nil
}
func (a *optionsAgent) Stop() error             { return nil }
func (a *optionsAgent) UsesPromptOptions() bool { return true }
func (a *optionsAgent) SetModel(m string)       { a.setModel = m }

type optionsSession struct {
	agent *optionsAgent
}

func (s *optionsSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	s.agent.sendCalls++
	return nil
}
func (s *optionsSession) SendWithOptions(prompt string, images []core.ImageAttachment, files []core.FileAttachment, opts core.PromptOptions) error {
	s.agent.gotOpts = opts
	s.agent.sendCalls++
	return nil
}
func (s *optionsSession) RespondPermission(string, core.PermissionResult) error { return nil }
func (s *optionsSession) Events() <-chan core.Event                             { return nil }
func (s *optionsSession) CurrentSessionID() string                              { return "" }
func (s *optionsSession) Alive() bool                                           { return true }
func (s *optionsSession) Close() error                                          { return nil }
func (s *optionsSession) RespondQuestion(string, []string) error                { return nil }
func (s *optionsSession) RejectQuestion(string) error                           { return nil }

// TestSendPromptDispatchesOptionsAtomically: an option-sender session
// receives SendWithOptions with the request's exact options; a plain session
// keeps Send semantics (§6.11.1 item 5 — no agent-global mutation).
func TestSendPromptDispatchesOptionsAtomically(t *testing.T) {
	agent := &optionsAgent{}
	opts := core.PromptOptions{Agent: "build", ProviderID: "p", ModelID: "m", Variant: "v"}
	sess := &optionsSession{agent: agent}
	if err := sendPrompt(sess, "x", nil, nil, opts); err != nil {
		t.Fatalf("sendPrompt: %v", err)
	}
	if agent.sendCalls != 1 || agent.gotOpts != opts {
		t.Fatalf("SendWithOptions must carry the exact options once, calls=%d opts=%+v", agent.sendCalls, agent.gotOpts)
	}
	if !isPromptOptionsSender(agent) {
		t.Fatal("isPromptOptionsSender must detect the PromptOptionsAgent")
	}
	// applySendMessageRuntimeOptions must NOT SetModel an option sender.
	applySendMessageRuntimeOptions(agent, SendMessageParams{Model: map[string]interface{}{"id": "other"}}, "")
	if agent.setModel != "" {
		t.Fatalf("option senders must not receive agent-global SetModel, got %q", agent.setModel)
	}
}

// TestListModelsWireItemCarriesVariants: model items expose exactly the live
// variant keys (§6.11.1 item 1).
func TestListModelsWireItemCarriesVariants(t *testing.T) {
	fake := &optionsAgent{}
	variants := []core.ModelOption{
		{Name: "localmock/echo", Desc: "Echo", Variants: []string{"high", "low"}},
		{Name: "localmock/alpha", Desc: "Alpha"},
	}
	items := modelItemsForWire(fake, variants, "localmock/echo")
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	echo := items[0]
	v, ok := echo["variants"].([]string)
	if !ok || len(v) != 2 || v[0] != "high" || v[1] != "low" {
		t.Fatalf("echo variants = %#v", echo["variants"])
	}
	if _, has := items[1]["variants"]; has {
		t.Fatal("alpha declares no variants — the key must be absent, not empty")
	}
	if items[0]["isDefault"] != true || items[1]["isDefault"] != false {
		t.Fatalf("isDefault mapping = %+v", items)
	}
}

// TestProtocolMirrorInSync: the Mac canonical protocol pack and the iOS
// mirror must be byte-identical for every file this batch touched (canonical
// §6.11.1 ordering: Mac canonical-first, iOS mirror a synchronized consumer).
func TestProtocolMirrorInSync(t *testing.T) {
	mirror := os.Getenv("CORDCODE_IOS_MIRROR")
	if mirror == "" {
		mirror = "../cordcode-ios/docs/protocol"
	}
	if _, err := os.Stat(mirror); err != nil {
		t.Skipf("iOS protocol mirror not present at %s — set CORDCODE_IOS_MIRROR", mirror)
	}
	for _, rel := range []string{
		"bridge-v1.md",
		filepath.Join("schema", "bridge-v1.types.ts"),
	} {
		macBytes, err := os.ReadFile(filepath.Join("..", "docs", "protocol", rel))
		if err != nil {
			t.Fatalf("read Mac canonical %s: %v", rel, err)
		}
		iosBytes, err := os.ReadFile(filepath.Join(mirror, rel))
		if err != nil {
			t.Fatalf("read iOS mirror %s: %v", rel, err)
		}
		if string(macBytes) != string(iosBytes) {
			firstDiff := firstLineDiff(string(macBytes), string(iosBytes))
			t.Fatalf("protocol mirror drift in %s (first divergence near %q) — resync the iOS mirror from the Mac canonical", rel, firstDiff)
		}
	}
}

func firstLineDiff(a, b string) string {
	aLines, bLines := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(aLines) && i < len(bLines); i++ {
		if aLines[i] != bLines[i] {
			return aLines[i]
		}
	}
	return "<tail>"
}

// TestListModelsWireItemCarriesResolvedAndObserved: claudecode Phase 1 三键
// (canonical additive) —— resolved / observedModel 是可选键，仅真值存在时下发。
func TestListModelsWireItemCarriesResolvedAndObserved(t *testing.T) {
	fake := &optionsAgent{}
	models := []core.ModelOption{
		{Name: "sonnet", Desc: "Sonnet", Resolved: "claude-sonnet-5", Observed: "glm-5.3"},
		{Name: "haiku", Desc: "Haiku"},
	}
	items := modelItemsForWire(fake, models, "sonnet")
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if items[0]["resolved"] != "claude-sonnet-5" || items[0]["observedModel"] != "glm-5.3" {
		t.Fatalf("sonnet three-key row = %+v", items[0])
	}
	if _, has := items[1]["resolved"]; has {
		t.Fatal("haiku declares no resolved — the key must be absent, not empty")
	}
	if _, has := items[1]["observedModel"]; has {
		t.Fatal("haiku declares no observed — the key must be absent, not empty")
	}
}
