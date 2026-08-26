package gobridge

import (
	"encoding/base64"
	"testing"
)

// web_push_subscription_test.go — DTO/校验单元（内部行为；外部形状定稿门 = WP-SUB-1 真机样本）。

func testVapidKeyPairB2() (pubBase64URL string) {
	// 确定性 65-byte uncompressed 形状（非真实曲线点——仅形状校验，不用于加密）。
	raw := make([]byte, 65)
	raw[0] = 0x04
	for i := 1; i < 65; i++ {
		raw[i] = byte(i * 7)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func validRegisterParamsB2(key string) RegisterPushSubscriptionParams {
	return RegisterPushSubscriptionParams{
		SchemaVersion:        1,
		Platform:             "ios-pwa",
		ApplicationServerKey: key,
		Subscription: WebPushSubscriptionWire{
			Endpoint:       "https://web.push.apple.comfixture/QHRhchDIkyotsBBB-xNy",
			ExpirationTime: nil,
			Keys: WebPushSubscriptionKeys{
				P256dh: base64.RawURLEncoding.EncodeToString(make([]byte, 65)),
				Auth:   base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
			},
		},
	}
}

func TestWebPushRegisterParamsValidateOK(t *testing.T) {
	key := testVapidKeyPairB2()
	params := validRegisterParamsB2(key)
	record, err := ValidateRegisterPushSubscriptionParams(&params, 512, key, 1000)
	if err != nil {
		t.Fatalf("expected valid, got %+v", err)
	}
	if record.Endpoint != params.Subscription.Endpoint {
		t.Fatalf("endpoint not preserved")
	}
	if record.DeviceID != "" {
		t.Fatalf("DeviceID must come from the authenticated connection, never params")
	}
}

func TestWebPushRegisterParamsSchemaAndSize(t *testing.T) {
	key := testVapidKeyPairB2()
	cases := []struct {
		name   string
		mutate func(p *RegisterPushSubscriptionParams)
		size   int
	}{
		{"wrong schema", func(p *RegisterPushSubscriptionParams) { p.SchemaVersion = 2 }, 512},
		{"empty platform", func(p *RegisterPushSubscriptionParams) { p.Platform = "" }, 512},
		{"oversize platform", func(p *RegisterPushSubscriptionParams) { p.Platform = string(make([]byte, 100)) }, 512},
		{"oversize params", func(p *RegisterPushSubscriptionParams) {}, WebPushMaxParamsBytes + 1},
		{"zero params", func(p *RegisterPushSubscriptionParams) {}, 0},
	}
	for _, tc := range cases {
		params := validRegisterParamsB2(key)
		tc.mutate(&params)
		if _, err := ValidateRegisterPushSubscriptionParams(&params, tc.size, key, 0); err == nil || err.code != WebPushErrInvalidSubscription {
			t.Fatalf("%s: expected invalid_subscription, got %+v", tc.name, err)
		}
	}
}

func TestWebPushRegisterParamsVapidKeyMismatch(t *testing.T) {
	key := testVapidKeyPairB2()
	other := make([]byte, 65)
	other[0] = 0x04
	for i := 1; i < 65; i++ {
		other[i] = byte(i * 13)
	}
	params := validRegisterParamsB2(base64.RawURLEncoding.EncodeToString(other))
	_, err := ValidateRegisterPushSubscriptionParams(&params, 512, key, 0)
	if err == nil || err.code != WebPushErrVapidKeyMismatch {
		t.Fatalf("expected vapid_key_mismatch, got %+v", err)
	}

	// 非 base64url 的 key 声明 → invalid_subscription。
	params.ApplicationServerKey = "not+base64/url=="
	if _, err := ValidateRegisterPushSubscriptionParams(&params, 512, key, 0); err == nil || err.code != WebPushErrInvalidSubscription {
		t.Fatalf("expected invalid_subscription for bad encoding, got %+v", err)
	}

	// 本机 key 无效（长度错误）→ unsupported（fail closed，不泄露比对结果）。
	badLocal := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if _, err := ValidateRegisterPushSubscriptionParams(&params, 512, badLocal, 0); err == nil || err.code != WebPushErrUnsupported {
		t.Fatalf("expected unsupported for invalid local key, got %+v", err)
	}
}

func TestWebPushRegisterParamsEndpointRules(t *testing.T) {
	key := testVapidKeyPairB2()
	cases := []struct {
		name     string
		endpoint string
	}{
		{"http rejected", "http://web.push.example.comfixture/token"},
		{"no host", "https:///token"},
		{"not a url", "://bad"},
		{"empty", ""},
		{"oversize", "https://web.push.example.comfixture/" + string(make([]byte, WebPushMaxEndpointBytes))},
	}
	for _, tc := range cases {
		params := validRegisterParamsB2(key)
		params.Subscription.Endpoint = tc.endpoint
		if _, err := ValidateRegisterPushSubscriptionParams(&params, 512, key, 0); err == nil || err.code != WebPushErrInvalidSubscription {
			t.Fatalf("%s: expected invalid_subscription, got %+v", tc.name, err)
		}
	}
}

func TestWebPushRegisterParamsKeyMaterialRules(t *testing.T) {
	key := testVapidKeyPairB2()
	for _, which := range []string{"p256dh", "auth"} {
		params := validRegisterParamsB2(key)
		if which == "p256dh" {
			params.Subscription.Keys.P256dh = ""
		} else {
			params.Subscription.Keys.Auth = "has+/padding=="
		}
		if _, err := ValidateRegisterPushSubscriptionParams(&params, 512, key, 0); err == nil || err.code != WebPushErrInvalidSubscription {
			t.Fatalf("%s: expected invalid_subscription, got %+v", which, err)
		}
	}
	// 超长 key 材料。
	params := validRegisterParamsB2(key)
	params.Subscription.Keys.P256dh = string(make([]byte, WebPushMaxKeyChars+1))
	if _, err := ValidateRegisterPushSubscriptionParams(&params, WebPushMaxParamsBytes, key, 0); err == nil || err.code != WebPushErrInvalidSubscription {
		t.Fatalf("oversize p256dh: expected invalid_subscription, got %+v", err)
	}
}

func TestWebPushSubscriptionIDStableAndShaped(t *testing.T) {
	a := BuildWebPushSubscriptionID("dev-1", "https://x.example/1")
	b := BuildWebPushSubscriptionID("dev-1", "https://x.example/1")
	c := BuildWebPushSubscriptionID("dev-2", "https://x.example/1")
	if a != b {
		t.Fatal("same device+endpoint must produce the same subscriptionId (idempotent upsert)")
	}
	if a == c {
		t.Fatal("different devices must not collide")
	}
	if len(a) != 4+webPushSubscriptionIDLen || a[:4] != "wps_" {
		t.Fatalf("subscriptionId shape: %q", a)
	}
}

func TestWebPushUnregisterParamsValidate(t *testing.T) {
	if _, err := ValidateUnregisterPushSubscriptionParams(&UnregisterPushSubscriptionParams{SchemaVersion: 1, SubscriptionID: "wps_0123456789abcdef"}, 64); err != nil {
		t.Fatalf("valid id rejected: %+v", err)
	}
	bad := []string{"", "wps_", "wps_ZZZZZZZZZZZZZZZZ", "sub_0123456789abcdef", "wps_0123456789abcde"}
	for _, id := range bad {
		if _, err := ValidateUnregisterPushSubscriptionParams(&UnregisterPushSubscriptionParams{SchemaVersion: 1, SubscriptionID: id}, 64); err == nil {
			t.Fatalf("id %q must be rejected", id)
		}
	}
	if _, err := ValidateUnregisterPushSubscriptionParams(&UnregisterPushSubscriptionParams{SchemaVersion: 2, SubscriptionID: "wps_0123456789abcdef"}, 64); err == nil {
		t.Fatal("wrong schemaVersion must be rejected")
	}
}

func TestWebPushEndpointHostCategoryNeverIncludesPath(t *testing.T) {
	endpoint := "https://web.push.apple.comfixture/SECRET-TOKEN?query=1"
	category := WebPushEndpointHostCategory(endpoint)
	if category != "apple" {
		t.Fatalf("expected apple category, got %q", category)
	}
	if len(category) >= len(endpoint) {
		t.Fatal("category must be a coarse host bucket, not the endpoint")
	}
	if WebPushEndpointHostCategory("::::not-a-url") != "unparseable" {
		t.Fatal("garbage endpoint must be unparseable")
	}
}

func TestDecodeBase64URLStrict(t *testing.T) {
	if _, err := DecodeBase64URL(""); err == nil {
		t.Fatal("empty must fail")
	}
	if _, err := DecodeBase64URL("AB+CD/EFG="); err == nil {
		t.Fatal("standard base64 alphabet must fail")
	}
	raw, err := DecodeBase64URL("AQIDBA")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 4 || raw[0] != 1 {
		t.Fatalf("unexpected decode: %v", raw)
	}
}
