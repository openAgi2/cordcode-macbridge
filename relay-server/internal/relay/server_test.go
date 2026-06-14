package relay

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const provisionToken = "provision-secret"

type testCredentials struct {
	routeID    string
	bridgeAuth string
	deviceAuth string
}

func newTestServer(t *testing.T, rate int) (*Server, *httptest.Server) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server, err := NewServer(store, Config{
		PublicEndpoint:       "wss://relay.example.test:8443",
		ProvisionTokenDigest: CredentialDigest(provisionToken),
		MailboxTTL:           time.Hour,
		MaxMailboxBytes:      4096,
		MaxFrameBytes:        4096,
		RateLimitPerMinute:   rate,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

func TestNewServerDefaultsFrameLimitForEncryptedPagination(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server, err := NewServer(store, Config{
		PublicEndpoint:       "wss://relay.example.test:8443",
		ProvisionTokenDigest: CredentialDigest(provisionToken),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if server.config.MaxFrameBytes != 2<<20 {
		t.Fatalf("default MaxFrameBytes = %d, want %d", server.config.MaxFrameBytes, 2<<20)
	}
}

func requestJSON(t *testing.T, method, address, auth string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, address, reader)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		request.Header.Set("Authorization", "Bearer "+auth)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func provisionDevice(t *testing.T, base string) testCredentials {
	t.Helper()
	response, data := requestJSON(t, http.MethodPost, base+"/v1/routes/register", provisionToken, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register route status=%d body=%s", response.StatusCode, data)
	}
	var route struct {
		RouteID    string `json:"routeId"`
		BridgeAuth string `json:"bridgeAuth"`
	}
	if err := json.Unmarshal(data, &route); err != nil {
		t.Fatal(err)
	}
	response, data = requestJSON(t, http.MethodPost, base+"/v1/routes/"+route.RouteID+"/devices/register", route.BridgeAuth, map[string]string{"deviceId": "phone-1"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register device status=%d body=%s", response.StatusCode, data)
	}
	var device struct {
		DeviceAuth string `json:"deviceAuth"`
	}
	if err := json.Unmarshal(data, &device); err != nil {
		t.Fatal(err)
	}
	return testCredentials{routeID: route.RouteID, bridgeAuth: route.BridgeAuth, deviceAuth: device.DeviceAuth}
}

func wsDial(t *testing.T, base, path, auth string) *websocket.Conn {
	t.Helper()
	return wsDialWithHeader(t, base, path, http.Header{"Authorization": []string{"Bearer " + auth}})
}

func wsDialWithHeader(t *testing.T, base, path string, header http.Header) *websocket.Conn {
	t.Helper()
	address, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	address.Scheme = "ws"
	address.Path = path
	conn, response, err := websocket.DefaultDialer.Dial(address.String(), header)
	if err != nil {
		var status int
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("websocket dial status=%d err=%v", status, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestRouteProvisioningRequiresTokenAndRateLimits(t *testing.T) {
	_, httpServer := newTestServer(t, 1)
	response, _ := requestJSON(t, http.MethodPost, httpServer.URL+"/v1/routes/register", "wrong", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodPost, httpServer.URL+"/v1/routes/register", provisionToken, nil)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second attempt should be rate limited, status=%d", response.StatusCode)
	}
}

func TestSignedActivationCreatesAndRecoversRouteWithoutDeploymentToken(t *testing.T) {
	_, httpServer := newTestServer(t, 10)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	installID := "install_new_mac"
	firstCredential := "first_bridge_credential_abcdefghijklmnopqrstuvwxyz"
	response, data := activateRoute(t, httpServer.URL, installID, firstCredential, publicKey, privateKey)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("activate route status=%d body=%s", response.StatusCode, data)
	}
	var first struct {
		RouteID string `json:"routeId"`
	}
	if err := json.Unmarshal(data, &first); err != nil {
		t.Fatal(err)
	}
	response, _ = requestJSON(t, http.MethodGet, httpServer.URL+"/v1/routes/"+first.RouteID+"/status", firstCredential, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("activated credential status=%d", response.StatusCode)
	}

	recoveredCredential := "recovered_bridge_credential_abcdefghijklmnopqr"
	response, data = activateRoute(t, httpServer.URL, installID, recoveredCredential, publicKey, privateKey)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("recover route status=%d body=%s", response.StatusCode, data)
	}
	var recovered struct {
		RouteID string `json:"routeId"`
	}
	if err := json.Unmarshal(data, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.RouteID != first.RouteID {
		t.Fatalf("activation recovery created a different route: %s != %s", recovered.RouteID, first.RouteID)
	}
	response, _ = requestJSON(t, http.MethodGet, httpServer.URL+"/v1/routes/"+first.RouteID+"/status", firstCredential, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("previous credential should be rotated, status=%d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodGet, httpServer.URL+"/v1/routes/"+first.RouteID+"/status", recoveredCredential, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recovered credential status=%d", response.StatusCode)
	}
}

func TestSignedActivationRejectsTamperingAndIdentityReplacement(t *testing.T) {
	_, httpServer := newTestServer(t, 10)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	response, data := activateRoute(t, httpServer.URL, "install_bound", "bridge_credential_abcdefghijklmnopqrstuvwxyz12", publicKey, privateKey)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("activate route status=%d body=%s", response.StatusCode, data)
	}

	otherPublicKey, otherPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	response, _ = activateRoute(t, httpServer.URL, "install_bound", "bridge_credential_abcdefghijklmnopqrstuvwxyz34", otherPublicKey, otherPrivateKey)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("replacement identity status=%d", response.StatusCode)
	}

	body := signedActivationBody("install_tampered", "bridge_credential_abcdefghijklmnopqrstuvwxyz56", publicKey, privateKey)
	body["bridgeAuth"] = "bridge_credential_attacker_changed_abcdefghi"
	response, _ = requestJSON(t, http.MethodPost, httpServer.URL+"/v1/activations/routes", "", body)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered credential status=%d", response.StatusCode)
	}
}

func activateRoute(t *testing.T, base, installID, bridgeAuth string, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) (*http.Response, []byte) {
	t.Helper()
	return requestJSON(t, http.MethodPost, base+"/v1/activations/routes", "", signedActivationBody(installID, bridgeAuth, publicKey, privateKey))
}

func signedActivationBody(installID, bridgeAuth string, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) map[string]any {
	timestamp := time.Now().Unix()
	nonce := "nonce_request_123"
	encodedPublicKey := base64.StdEncoding.EncodeToString(publicKey)
	signature := ed25519.Sign(privateKey, activationPayload(installID, encodedPublicKey, bridgeAuth, timestamp, nonce))
	return map[string]any{
		"installId":  installID,
		"publicKey":  encodedPublicKey,
		"bridgeAuth": bridgeAuth,
		"timestamp":  timestamp,
		"nonce":      nonce,
		"signature":  base64.StdEncoding.EncodeToString(signature),
	}
}

func TestMailboxRequestsAreRateLimitedPerOperation(t *testing.T) {
	_, httpServer := newTestServer(t, 1)
	credentials := provisionDevice(t, httpServer.URL)
	address := httpServer.URL + "/v1/routes/" + credentials.routeID + "/devices/phone-1/mailbox"
	response, _ := requestJSON(t, http.MethodGet, address, credentials.deviceAuth, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("first mailbox request status=%d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodGet, address, credentials.deviceAuth, nil)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second mailbox request should be rate limited, status=%d", response.StatusCode)
	}
}

func TestOnlineForwardingAndStatusDoesNotExposeSecrets(t *testing.T) {
	_, httpServer := newTestServer(t, 10)
	credentials := provisionDevice(t, httpServer.URL)
	bridge := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/bridge", credentials.bridgeAuth)
	device := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/devices/phone-1", credentials.deviceAuth)
	toDevice := []byte(`{"routeId":"` + credentials.routeID + `","senderId":"bridge","destinationId":"phone-1","ciphertext":"cipher"}`)
	if err := bridge.WriteMessage(websocket.TextMessage, toDevice); err != nil {
		t.Fatal(err)
	}
	_, payload, err := device.ReadMessage()
	if err != nil || string(payload) != string(toDevice) {
		t.Fatalf("device payload=%s err=%v", payload, err)
	}
	toBridge := []byte(`{"routeId":"` + credentials.routeID + `","senderId":"phone-1","destinationId":"bridge","ciphertext":"reply"}`)
	if err := device.WriteMessage(websocket.TextMessage, toBridge); err != nil {
		t.Fatal(err)
	}
	_, payload, err = bridge.ReadMessage()
	if err != nil || string(payload) != string(toBridge) {
		t.Fatalf("bridge payload=%s err=%v", payload, err)
	}
	response, data := requestJSON(t, http.MethodGet, httpServer.URL+"/v1/routes/"+credentials.routeID+"/status", credentials.bridgeAuth, nil)
	if response.StatusCode != http.StatusOK || strings.Contains(string(data), credentials.bridgeAuth) || strings.Contains(string(data), credentials.deviceAuth) {
		t.Fatalf("unsafe status response status=%d body=%s", response.StatusCode, data)
	}
}

func TestDeviceWebSocketAuthViaQueryParam(t *testing.T) {
	_, httpServer := newTestServer(t, 10)
	credentials := provisionDevice(t, httpServer.URL)

	// 不带认证的 WebSocket 连接应被拒绝
	_, httpServerURL, _ := strings.Cut(httpServer.URL, "://")
	wsURL := "ws://" + httpServerURL + "/v1/routes/" + credentials.routeID + "/devices/phone-1"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("no-auth dial should fail")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth status=%d, want 401", resp.StatusCode)
	}

	// 通过 query 参数传递 token 应能认证成功
	wsURLWithToken := "ws://" + httpServerURL + "/v1/routes/" + credentials.routeID + "/devices/phone-1?token=" + url.QueryEscape(credentials.deviceAuth)
	device, _, err := websocket.DefaultDialer.Dial(wsURLWithToken, nil)
	if err != nil {
		t.Fatalf("query-param auth dial failed: %v", err)
	}
	defer device.Close()

	// bridge 也连接，验证双向转发
	bridge := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/bridge", credentials.bridgeAuth)
	msg := []byte(`{"routeId":"` + credentials.routeID + `","senderId":"bridge","destinationId":"phone-1","ciphertext":"via-query"}`)
	if err := bridge.WriteMessage(websocket.TextMessage, msg); err != nil {
		t.Fatal(err)
	}
	_, payload, err := device.ReadMessage()
	if err != nil || string(payload) != string(msg) {
		t.Fatalf("query-param device payload=%s err=%v", payload, err)
	}
}

func TestOnlineHandshakeFramesForwardOnlyToTargetDevice(t *testing.T) {
	_, httpServer := newTestServer(t, 10)
	credentials := provisionDevice(t, httpServer.URL)
	response, data := requestJSON(t, http.MethodPost, httpServer.URL+"/v1/routes/"+credentials.routeID+"/devices/register", credentials.bridgeAuth, map[string]string{"deviceId": "phone-2"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register second device status=%d body=%s", response.StatusCode, data)
	}
	var second struct {
		DeviceAuth string `json:"deviceAuth"`
	}
	if err := json.Unmarshal(data, &second); err != nil {
		t.Fatal(err)
	}

	bridge := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/bridge", credentials.bridgeAuth)
	defer bridge.Close()
	device := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/devices/phone-1", credentials.deviceAuth)
	defer device.Close()
	otherDevice := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/devices/phone-2", second.DeviceAuth)
	defer otherDevice.Close()

	clientHello := []byte(`{"type":"online_client_hello","deviceId":"phone-1","bridgeId":"bridge-1"}`)
	if err := device.WriteMessage(websocket.TextMessage, clientHello); err != nil {
		t.Fatal(err)
	}
	_, payload, err := bridge.ReadMessage()
	if err != nil || string(payload) != string(clientHello) {
		t.Fatalf("bridge handshake payload=%s err=%v", payload, err)
	}

	serverHello := []byte(`{"type":"online_server_hello","deviceId":"phone-1","bridgeId":"bridge-1"}`)
	if err := bridge.WriteMessage(websocket.TextMessage, serverHello); err != nil {
		t.Fatal(err)
	}
	_, payload, err = device.ReadMessage()
	if err != nil || string(payload) != string(serverHello) {
		t.Fatalf("target device handshake payload=%s err=%v", payload, err)
	}

	if err := otherDevice.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, payload, err = otherDevice.ReadMessage(); err == nil {
		t.Fatalf("other device unexpectedly received handshake payload=%s", payload)
	}
}

func TestOfflineMailboxFetchAckAndRevokeAPI(t *testing.T) {
	_, httpServer := newTestServer(t, 10)
	credentials := provisionDevice(t, httpServer.URL)
	bridge := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/bridge", credentials.bridgeAuth)
	envelope := []byte(`{"routeId":"` + credentials.routeID + `","senderId":"bridge","destinationId":"phone-1","ciphertext":"offline"}`)
	if err := bridge.WriteMessage(websocket.TextMessage, envelope); err != nil {
		t.Fatal(err)
	}
	var mailbox struct {
		Frames []MailboxFrame `json:"frames"`
	}
	var response *http.Response
	var data []byte
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		response, data = requestJSON(t, http.MethodGet, httpServer.URL+"/v1/routes/"+credentials.routeID+"/devices/phone-1/mailbox", credentials.deviceAuth, nil)
		_ = json.Unmarshal(data, &mailbox)
		if len(mailbox.Frames) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if response.StatusCode != http.StatusOK || len(mailbox.Frames) != 1 || string(mailbox.Frames[0].Envelope) != string(envelope) {
		t.Fatalf("mailbox status=%d body=%s", response.StatusCode, data)
	}
	response, data = requestJSON(t, http.MethodPost, httpServer.URL+"/v1/routes/"+credentials.routeID+"/devices/phone-1/mailbox/ack", credentials.deviceAuth, map[string]uint64{"through": mailbox.Frames[0].Cursor})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ack status=%d body=%s", response.StatusCode, data)
	}
	response, data = requestJSON(t, http.MethodPost, httpServer.URL+"/v1/routes/"+credentials.routeID+"/devices/phone-1/revoke", credentials.bridgeAuth, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", response.StatusCode, data)
	}
	response, _ = requestJSON(t, http.MethodGet, httpServer.URL+"/v1/routes/"+credentials.routeID+"/devices/phone-1/mailbox", credentials.deviceAuth, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked credential status=%d", response.StatusCode)
	}
}

func TestMailboxEpochIsStoredWithoutOnlineForward(t *testing.T) {
	_, httpServer := newTestServer(t, 10)
	credentials := provisionDevice(t, httpServer.URL)
	bridge := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/bridge", credentials.bridgeAuth)
	defer bridge.Close()
	device := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/devices/phone-1", credentials.deviceAuth)
	defer device.Close()

	envelope := []byte(`{"routeId":"` + credentials.routeID + `","senderId":"bridge","destinationId":"phone-1","keyEpochId":"mailbox:0","ciphertext":"offline"}`)
	if err := bridge.WriteMessage(websocket.TextMessage, envelope); err != nil {
		t.Fatal(err)
	}

	if err := device.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, payload, err := device.ReadMessage(); err == nil {
		t.Fatalf("online device unexpectedly received mailbox epoch payload=%s", payload)
	}

	var mailbox struct {
		Frames []MailboxFrame `json:"frames"`
	}
	response, data := requestJSON(t, http.MethodGet, httpServer.URL+"/v1/routes/"+credentials.routeID+"/devices/phone-1/mailbox", credentials.deviceAuth, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mailbox status=%d body=%s", response.StatusCode, data)
	}
	if err := json.Unmarshal(data, &mailbox); err != nil {
		t.Fatal(err)
	}
	if len(mailbox.Frames) != 1 || string(mailbox.Frames[0].Envelope) != string(envelope) {
		t.Fatalf("mailbox body=%s", data)
	}
}

func TestPairingClaimLifecycle(t *testing.T) {
	_, httpServer := newTestServer(t, 0)
	credentials := provisionDevice(t, httpServer.URL)
	base := httpServer.URL

	claimID := "claim_test123"
	capability := "cap_secret_token"
	sealedClaim := []byte("encrypted-claim-data")

	// 提交配对请求
	response, data := requestJSON(t, http.MethodPost,
		base+"/v1/routes/"+credentials.routeID+"/pairing-claims", "",
		map[string]any{"claimId": claimID, "capability": capability, "sealedClaim": sealedClaim})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("submit claim status=%d body=%s", response.StatusCode, data)
	}
	var submit map[string]string
	if err := json.Unmarshal(data, &submit); err != nil {
		t.Fatal(err)
	}
	if submit["claimId"] != claimID || submit["state"] != "pending" {
		t.Fatalf("unexpected submit response: %v", submit)
	}

	// 未认证列出应失败
	response, _ = requestJSON(t, http.MethodGet,
		base+"/v1/routes/"+credentials.routeID+"/pairing-claims", "wrong-auth", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth list status=%d", response.StatusCode)
	}

	// Mac 列出 pending claims
	response, data = requestJSON(t, http.MethodGet,
		base+"/v1/routes/"+credentials.routeID+"/pairing-claims", credentials.bridgeAuth, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list claims status=%d body=%s", response.StatusCode, data)
	}
	var listResult struct {
		Claims []PairingClaim `json:"claims"`
	}
	if err := json.Unmarshal(data, &listResult); err != nil {
		t.Fatal(err)
	}
	if len(listResult.Claims) != 1 || listResult.Claims[0].ClaimID != claimID {
		t.Fatalf("unexpected claims list: %v", listResult)
	}

	// 错误 capability 获取结果应失败
	response, _ = requestJSON(t, http.MethodGet,
		base+"/v1/routes/"+credentials.routeID+"/pairing-claims/"+claimID+"/result", "wrong-cap", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong cap result status=%d", response.StatusCode)
	}

	// Mac 批准
	sealedResult := []byte("encrypted-result-data")
	response, data = requestJSON(t, http.MethodPost,
		base+"/v1/routes/"+credentials.routeID+"/pairing-claims/"+claimID+"/complete", credentials.bridgeAuth,
		map[string]any{"state": "approved", "sealedResult": sealedResult})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("complete claim status=%d body=%s", response.StatusCode, data)
	}

	// 获取结果
	response, data = requestJSON(t, http.MethodGet,
		base+"/v1/routes/"+credentials.routeID+"/pairing-claims/"+claimID+"/result", capability, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get result status=%d body=%s", response.StatusCode, data)
	}
	var result PairingClaim
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "approved" || string(result.SealedResult) != string(sealedResult) {
		t.Fatalf("unexpected result: state=%s sealed=%v", result.State, result.SealedResult)
	}

	// 重复获取应 404（已 consume）
	response, _ = requestJSON(t, http.MethodGet,
		base+"/v1/routes/"+credentials.routeID+"/pairing-claims/"+claimID+"/result", capability, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("consumed claim should be 404, got %d", response.StatusCode)
	}
}

func TestPairingClaimReject(t *testing.T) {
	_, httpServer := newTestServer(t, 0)
	credentials := provisionDevice(t, httpServer.URL)

	claimID := "claim_reject"
	capability := "cap_reject_secret"

	response, _ := requestJSON(t, http.MethodPost,
		httpServer.URL+"/v1/routes/"+credentials.routeID+"/pairing-claims", "",
		map[string]any{"claimId": claimID, "capability": capability, "sealedClaim": []byte("claim")})
	if response.StatusCode != http.StatusOK {
		t.Fatal("submit failed")
	}

	response, _ = requestJSON(t, http.MethodPost,
		httpServer.URL+"/v1/routes/"+credentials.routeID+"/pairing-claims/"+claimID+"/complete", credentials.bridgeAuth,
		map[string]any{"state": "rejected"})
	if response.StatusCode != http.StatusOK {
		t.Fatal("reject failed")
	}

	response, data := requestJSON(t, http.MethodGet,
		httpServer.URL+"/v1/routes/"+credentials.routeID+"/pairing-claims/"+claimID+"/result", capability, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("result status=%d body=%s", response.StatusCode, data)
	}
	var result PairingClaim
	json.Unmarshal(data, &result)
	if result.State != "rejected" {
		t.Fatalf("expected rejected, got %s", result.State)
	}
}

func TestPairingClaimInvalidInput(t *testing.T) {
	_, httpServer := newTestServer(t, 0)
	credentials := provisionDevice(t, httpServer.URL)

	// 缺少 claimId
	response, _ := requestJSON(t, http.MethodPost,
		httpServer.URL+"/v1/routes/"+credentials.routeID+"/pairing-claims", "",
		map[string]any{"capability": "cap", "sealedClaim": []byte("x")})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.StatusCode)
	}

	// 无效 state
	response, _ = requestJSON(t, http.MethodPost,
		httpServer.URL+"/v1/routes/"+credentials.routeID+"/pairing-claims/claim1/complete", credentials.bridgeAuth,
		map[string]any{"state": "invalid"})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid state, got %d", response.StatusCode)
	}

	// 不存在的 claim
	response, _ = requestJSON(t, http.MethodPost,
		httpServer.URL+"/v1/routes/"+credentials.routeID+"/pairing-claims/nonexistent/complete", credentials.bridgeAuth,
		map[string]any{"state": "approved", "sealedResult": []byte("x")})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for nonexistent claim, got %d", response.StatusCode)
	}
}
