package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// web_push_protocol_contract_test.go — canonical contract consistency (impl-plan 阶段 A/Gate A)。
// 验证 Go 常量、docs/protocol canonical Markdown、schema types 与 samples/web-push fixture
// 四方一致；两个 bridge 级 RPC 只有一种（含必填 backendId 的标准）request envelope。

const webPushFixtureDir = "../docs/protocol/samples/web-push"

func loadWebPushFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(webPushFixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

// 契约：register/unregister 请求 fixture 使用标准 request envelope，backendId 必填且
// 不存在第二种无 backendId 的 request 形状。
func TestWebPushContractRequestEnvelopeIsCanonical(t *testing.T) {
	for _, fixture := range []string{
		"bridge-v1-request-register-push-subscription.json",
		"bridge-v1-request-unregister-push-subscription.json",
	} {
		var req struct {
			Type      string          `json:"type"`
			RequestID string          `json:"requestId"`
			BackendID string          `json:"backendId"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(loadWebPushFixture(t, fixture), &req); err != nil {
			t.Fatalf("%s: unmarshal: %v", fixture, err)
		}
		if req.Type != "request" {
			t.Fatalf("%s: type must be canonical \"request\", got %q", fixture, req.Type)
		}
		if req.RequestID == "" {
			t.Fatalf("%s: requestId must be non-empty", fixture)
		}
		if req.BackendID == "" {
			t.Fatalf("%s: backendId is required by the canonical envelope (server ignores its business semantics)", fixture)
		}
		switch req.Method {
		case WebPushMethodRegister, WebPushMethodUnregister:
		default:
			t.Fatalf("%s: method %q is not a web push contract method", fixture, req.Method)
		}
		var params struct {
			SchemaVersion int `json:"schemaVersion"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("%s: params unmarshal: %v", fixture, err)
		}
		if params.SchemaVersion != WebPushSchemaVersion {
			t.Fatalf("%s: schemaVersion must be %d, got %d", fixture, WebPushSchemaVersion, params.SchemaVersion)
		}
	}
}

// 契约：result fixture 与 RPC 常量、错误码表一致。
func TestWebPushContractResultFixtures(t *testing.T) {
	var reg struct {
		Type string `json:"type"`
		OK   bool   `json:"ok"`
		Data struct {
			SubscriptionID     string `json:"subscriptionId"`
			RegisteredAtMillis int64  `json:"registeredAtMillis"`
		} `json:"data"`
	}
	if err := json.Unmarshal(loadWebPushFixture(t, "bridge-v1-result-register-push-subscription.json"), &reg); err != nil {
		t.Fatal(err)
	}
	if reg.Type != "result" || !reg.OK || !strings.HasPrefix(reg.Data.SubscriptionID, "wps_") {
		t.Fatalf("register result fixture shape mismatch: %+v", reg)
	}

	var unreg struct {
		Data struct {
			Removed bool `json:"removed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(loadWebPushFixture(t, "bridge-v1-result-unregister-push-subscription.json"), &unreg); err != nil {
		t.Fatal(err)
	}
	if !unreg.Data.Removed {
		t.Fatalf("unregister result fixture must carry removed flag")
	}
}

// 契约：hello_ack fixture 的 webPush profile 与 Go 类型逐字段一致（含 misconfigured
// 约束——健康时才允许 vapidPublicKey）。
func TestWebPushContractHelloAckProfile(t *testing.T) {
	raw := loadWebPushFixture(t, "bridge-v1-hello-ack-web-push.json")
	var ack struct {
		Capabilities map[string]bool     `json:"capabilities"`
		WebPush      WebPushHelloProfile `json:"webPush"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatal(err)
	}
	if !ack.Capabilities[WebPushCapability] {
		t.Fatalf("fixture must echo %s", WebPushCapability)
	}
	if ack.WebPush.SchemaVersion != WebPushSchemaVersion {
		t.Fatalf("webPush.schemaVersion must be %d", WebPushSchemaVersion)
	}
	if ack.WebPush.VapidPublicKey == "" {
		t.Fatal("healthy profile fixture must carry vapidPublicKey")
	}
	if ack.WebPush.Status == WebPushStatusMisconfigured && ack.WebPush.VapidPublicKey != "" {
		t.Fatal("misconfigured profile must not ship a (forged) public key")
	}
}

// 契约：SW payload schema v1 与 Go 类型往返一致；固定文案表覆盖四类 kind。
func TestWebPushContractPayloadSchema(t *testing.T) {
	var payload WebPushPayloadV1
	if err := json.Unmarshal(loadWebPushFixture(t, "sw-push-payload-v1.json"), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != WebPushSchemaVersion {
		t.Fatalf("payload schemaVersion must be %d", WebPushSchemaVersion)
	}
	if payload.Target.BridgeID == "" || payload.Target.BackendID == "" ||
		payload.Target.SessionID == "" || payload.Target.EventID == "" {
		t.Fatal("payload target must be fully described by bridgeId+backendId+sessionId+eventId")
	}
	if payload.Notification.Title == "" || payload.Notification.Body == "" || payload.Notification.Tag == "" {
		t.Fatal("payload notification must carry fixed title/body/tag")
	}
	for _, kind := range []WebPushNotificationKind{
		WebPushKindCompletion, WebPushKindPermission, WebPushKindInput, WebPushKindError,
	} {
		title, body := buildWebPushNotificationText(kind, "", "")
		if title == "" || body == "" {
			t.Fatalf("kind %q must have fixed title/body", kind)
		}
	}
	// 序列化往返：anchor null 必须保持显式 null（深链目标唯一描述的一部分）。
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"anchor":null`) {
		t.Fatal("anchor must serialize as explicit null when absent")
	}
}

// 契约：canonical Markdown 的 RPC 方法列表与 scope 表包含两个 web push RPC 与
// web_push.manage scope；canonical 是 mirror（iOS 仓）与 remote-web types 的比对源。
func TestWebPushContractCanonicalDocConsistency(t *testing.T) {
	raw, err := os.ReadFile("../docs/protocol/bridge-v1.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, method := range []string{WebPushMethodRegister, WebPushMethodUnregister} {
		if !strings.Contains(doc, method) {
			t.Fatalf("canonical bridge-v1.md must list %s", method)
		}
	}
	if !strings.Contains(doc, WebPushScope) {
		t.Fatalf("canonical bridge-v1.md must document scope %s", WebPushScope)
	}
	for _, code := range []string{
		WebPushErrUnsupported, WebPushErrInvalidSubscription,
		WebPushErrVapidKeyMismatch, WebPushErrStorageFailed,
	} {
		if !strings.Contains(doc, code) {
			t.Fatalf("canonical bridge-v1.md must document error code %s", code)
		}
	}
	if !strings.Contains(doc, WebPushCapability) {
		t.Fatalf("canonical bridge-v1.md must document capability %s", WebPushCapability)
	}
}

// 契约：schema/bridge-v1.types.ts（canonical TS reference）包含 web push additive 类型。
func TestWebPushContractSchemaTypesContainWebPush(t *testing.T) {
	raw, err := os.ReadFile("../docs/protocol/schema/bridge-v1.types.ts")
	if err != nil {
		t.Fatal(err)
	}
	types := string(raw)
	for _, fragment := range []string{
		"BridgeHelloAckWebPush",
		"BridgeRegisterPushSubscriptionParams",
		"BridgeUnregisterPushSubscriptionParams",
		"BridgeWebPushPayloadV1",
		"CORDCODE_PUSH_NAVIGATE_V1",
		`"web_push_v1"`,
	} {
		if !strings.Contains(types, fragment) {
			t.Fatalf("canonical schema types must contain %s", fragment)
		}
	}
}
