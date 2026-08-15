package gobridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Phase A 定向测试:connectionPolicy control-plane 管线 + 真实 relay.connected status provider。
// 覆盖评审 §7.1:wire shape、三落点(RelayFirstResult/pairing_complete/hello_ack dual-struct)、
// 缺字段兼容、policy/候选独立、/internal/remote/status 的 preferLocalNetwork+connected、
// provider 构造窗口 nil-safety + 透传、SSV2 control-plane-only 守卫。
//
// SSV2 红线贯穿本文件:connectionPolicy 永不进入 EventMessage.Data / timeline / SessionProjection /
// ProjectionReducer(见 TestConnectionPolicy_ControlPlaneOnly)。

// fakeRelayStatusProvider 用于测试 /internal/remote/status 的 relay.connected 透传,
// 模拟 *RelayBridgeClient 的 Connected()(它凭现有内部锁隐式满足 RelayConnectionStatusProvider)。
type fakeRelayStatusProvider struct{ connected bool }

func (f fakeRelayStatusProvider) Connected() bool { return f.connected }

func TestConnectionPolicy_JSONShape(t *testing.T) {
	out, err := json.Marshal(ConnectionPolicy{PreferLocalNetwork: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"preferLocalNetwork":true`) {
		t.Errorf("missing preferLocalNetwork:true in %s", out)
	}
	// 非指针 bool 字段(无 omitempty)恒发射:false 也作为权威值随每次握手下发。
	outFalse, _ := json.Marshal(ConnectionPolicy{})
	if !strings.Contains(string(outFalse), `"preferLocalNetwork":false`) {
		t.Errorf("zero-value ConnectionPolicy must still emit preferLocalNetwork:false (authoritative): %s", outFalse)
	}
}

func TestRelayFirstResult_ConnectionPolicy(t *testing.T) {
	withPolicy := RelayFirstResult{
		DeviceID:         "dev",
		ConnectionPolicy: &ConnectionPolicy{PreferLocalNetwork: true},
	}
	out, _ := json.Marshal(withPolicy)
	if !strings.Contains(string(out), `"connectionPolicy":{"preferLocalNetwork":true}`) {
		t.Errorf("RelayFirstResult missing connectionPolicy in %s", out)
	}
	// 指针 omitempty:nil policy 被省略(旧 payload 兼容)。
	without, _ := json.Marshal(RelayFirstResult{DeviceID: "dev"})
	if strings.Contains(string(without), "connectionPolicy") {
		t.Errorf("nil ConnectionPolicy must be omitted: %s", without)
	}
}

// RelayFirstResult 中 policy 与 LAN 候选必须独立:关偏好(false)时候选仍完整发布。
func TestRelayFirstResult_PolicyIndependentOfCandidates(t *testing.T) {
	r := RelayFirstResult{
		LocalURLs:        []string{"ws://192.168.1.25:8777/bridge"},
		ConnectionPolicy: &ConnectionPolicy{PreferLocalNetwork: false},
	}
	out, _ := json.Marshal(r)
	if !strings.Contains(string(out), `"localUrls"`) {
		t.Errorf("candidates must still be published when preferLocalNetwork=false: %s", out)
	}
	if !strings.Contains(string(out), `"connectionPolicy":{"preferLocalNetwork":false}`) {
		t.Errorf("policy must be present (false=authoritative Relay base): %s", out)
	}
}

// direct pairing_complete.bridge 保持扁平 URL 结构,policy 只做 round-trip,无 secondary LAN 数组。
func TestPairingCompleteBridge_ConnectionPolicyFlatURLs(t *testing.T) {
	remote := "wss://tail.example:8778/bridge"
	b := PairingCompleteBridge{
		BridgeID:         "brg",
		DisplayName:      "Mac",
		LocalURL:         "ws://192.168.1.25:8777/bridge",
		RemoteURL:        &remote,
		RemoteURLs:       []string{remote},
		ConnectionPolicy: &ConnectionPolicy{PreferLocalNetwork: false},
	}
	out, _ := json.Marshal(b)
	for _, key := range []string{`"bridgeId"`, `"localURL"`, `"remoteURL"`, `"remoteURLs"`, `"connectionPolicy":{"preferLocalNetwork":false}`} {
		if !strings.Contains(string(out), key) {
			t.Errorf("PairingCompleteBridge missing %s in %s", key, out)
		}
	}
	// 扁平结构:不得出现 secondary LAN 候选数组(那由认证 hello_ack.currentURLs.locals 刷新)。
	if strings.Contains(string(out), `"localUrls"`) || strings.Contains(string(out), `"locals"`) {
		t.Errorf("PairingCompleteBridge must stay flat (no secondary LAN array): %s", out)
	}
}

// round-3 P2-1 同类 dual-struct 守卫:runtime HelloBridgeInfo 与 schema BridgeV1BridgeProfile
// 都描述 hello_ack.bridge,connectionPolicy 必须序列化为同一 JSON 键且值一致,防漂移。
func TestConnectionPolicy_DualStructureConsistency(t *testing.T) {
	rt, _ := json.Marshal(HelloBridgeInfo{
		BridgeID:         "brg",
		CurrentURLs:      HelloURLs{Local: "l"},
		ConnectionPolicy: &ConnectionPolicy{PreferLocalNetwork: true},
	})
	sc, _ := json.Marshal(BridgeV1BridgeProfile{
		BridgeID:         "brg",
		CurrentURLs:      BridgeV1CurrentURLs{Local: "l"},
		ConnectionPolicy: &ConnectionPolicy{PreferLocalNetwork: true},
	})
	var rtMap, scMap map[string]json.RawMessage
	if err := json.Unmarshal(rt, &rtMap); err != nil {
		t.Fatalf("unmarshal runtime: %v", err)
	}
	if err := json.Unmarshal(sc, &scMap); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	rtP, rtOK := rtMap["connectionPolicy"]
	scP, scOK := scMap["connectionPolicy"]
	if !rtOK {
		t.Fatalf("HelloBridgeInfo missing connectionPolicy: %s", rt)
	}
	if !scOK {
		t.Fatalf("BridgeV1BridgeProfile missing connectionPolicy: %s", sc)
	}
	if string(rtP) != string(scP) {
		t.Errorf("connectionPolicy drift between runtime and schema: runtime=%s schema=%s", rtP, scP)
	}
}

// 缺字段兼容:旧 payload(无 connectionPolicy)解码为 nil policy,消费侧按 false 处理。
func TestConnectionPolicy_OldPayloadCompat(t *testing.T) {
	old := `{"bridgeId":"brg","currentURLs":{"local":"l"},"protocol":{"name":"cordcode-bridge","version":1}}`
	var info HelloBridgeInfo
	if err := json.Unmarshal([]byte(old), &info); err != nil {
		t.Fatalf("unmarshal old payload: %v", err)
	}
	if info.ConnectionPolicy != nil {
		t.Errorf("old payload must decode ConnectionPolicy=nil, got %+v", info.ConnectionPolicy)
	}
	prefer := false
	if info.ConnectionPolicy != nil {
		prefer = info.ConnectionPolicy.PreferLocalNetwork
	}
	if prefer {
		t.Errorf("nil policy must be treated as preferLocalNetwork=false")
	}
}

// SSV2 守卫:connectionPolicy 只允许存在于 4 个 control-plane 投递点,
// 不得出现在任何 timeline/projection/reducer 类型上。
func TestConnectionPolicy_ControlPlaneOnly(t *testing.T) {
	forbidden := []interface{}{
		EventMessage{},
		SessionProjection{},
		MessageProjection{},
		TurnProjection{},
		ProjectionReducer{},
	}
	for _, v := range forbidden {
		rt := reflect.TypeOf(v)
		if _, ok := rt.FieldByName("ConnectionPolicy"); ok {
			t.Errorf("%s must NOT carry a ConnectionPolicy field (SSV2 control-plane isolation)", rt.Name())
		}
	}
	allowed := []interface{}{
		HelloBridgeInfo{},
		BridgeV1BridgeProfile{},
		PairingCompleteBridge{},
		RelayFirstResult{},
	}
	for _, v := range allowed {
		rt := reflect.TypeOf(v)
		if _, ok := rt.FieldByName("ConnectionPolicy"); !ok {
			t.Errorf("%s MUST carry ConnectionPolicy (control-plane delivery point)", rt.Name())
		}
	}
}

// ── /internal/remote/status:preferLocalNetwork + 真实 relay.connected ──

type remoteStatusDecoded struct {
	PreferLocalNetwork bool `json:"preferLocalNetwork"`
	Relay              struct {
		Enabled    bool   `json:"enabled"`
		Configured bool   `json:"configured"`
		Connected  bool   `json:"connected"`
		Endpoint   string `json:"endpoint"`
		RouteID    string `json:"routeId"`
	} `json:"relay"`
}

func remoteStatusBody(t *testing.T, s *ManagementServer) remoteStatusDecoded {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleRemoteStatus(rec, httptest.NewRequest(http.MethodGet, "/internal/remote/status", nil))
	var d remoteStatusDecoded
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("unmarshal status: %v body=%s", err, rec.Body.String())
	}
	return d
}

// 构造窗口:ManagementServer.Start() 早于 RelayBridgeClient 构造,default disconnected provider
// 必须使 connected=false,即使 relay.configured=true。不从 configured 推导为已连接。
func TestRemoteStatus_ConstructionWindowDisconnected(t *testing.T) {
	cfg := ManagementConfig{
		LocalURL:           "ws://192.168.1.25:8777/bridge",
		RelayEnabled:       true,
		RelayConfigured:    true,
		RelayEndpoint:      "wss://relay.example",
		RelayRouteID:       "rt",
		PreferLocalNetwork: true,
	}
	s := NewManagementServer(cfg) // 未注入真实 provider

	body := remoteStatusBody(t, s)
	if body.Relay.Connected {
		t.Errorf("construction window must report connected=false (default disconnected provider)")
	}
	if !body.Relay.Configured {
		t.Errorf("expected configured=true")
	}
	if !body.PreferLocalNetwork {
		t.Errorf("expected preferLocalNetwork=true to pass through")
	}
	if body.Relay.Endpoint != "wss://relay.example" {
		t.Errorf("endpoint mismatch: %s", body.Relay.Endpoint)
	}
}

// 真实 provider 注入后,Connected() 的 true/false 透传到 endpoint。
func TestRemoteStatus_ProviderPassthrough(t *testing.T) {
	cfg := ManagementConfig{RelayEnabled: true, RelayConfigured: true}
	s := NewManagementServer(cfg)

	s.SetRelayStatusProvider(fakeRelayStatusProvider{connected: true})
	if body := remoteStatusBody(t, s); !body.Relay.Connected {
		t.Errorf("provider connected=true must reach endpoint")
	}

	s.SetRelayStatusProvider(fakeRelayStatusProvider{connected: false})
	if body := remoteStatusBody(t, s); body.Relay.Connected {
		t.Errorf("provider connected=false must reach endpoint")
	}
}

// Relay 未启用或未配置时,即使 provider 谎报 connected=true,endpoint 也必须为 false。
func TestRemoteStatus_DisabledUnconfiguredForcesFalse(t *testing.T) {
	cases := []struct {
		name string
		cfg  ManagementConfig
	}{
		{"disabled", ManagementConfig{RelayEnabled: false, RelayConfigured: true}},
		{"unconfigured", ManagementConfig{RelayEnabled: true, RelayConfigured: false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewManagementServer(c.cfg)
			s.SetRelayStatusProvider(fakeRelayStatusProvider{connected: true}) // provider 谎报 true
			body := remoteStatusBody(t, s)
			if body.Relay.Connected {
				t.Errorf("disabled/unconfigured must force connected=false regardless of provider")
			}
		})
	}
}

// SetRelayStatusProvider(nil) 回落到 default disconnected,不得 panic 或 nil deref。
func TestRemoteStatus_NilProviderSafe(t *testing.T) {
	s := NewManagementServer(ManagementConfig{RelayEnabled: true, RelayConfigured: true})
	s.SetRelayStatusProvider(nil)
	body := remoteStatusBody(t, s) // 不得 panic
	if body.Relay.Connected {
		t.Errorf("nil provider must yield connected=false")
	}
}
