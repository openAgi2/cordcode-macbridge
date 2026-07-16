package relay

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestLiveRelayDeployment 使用显式注入的生产端点和 provisioning token 做公网验收。
// 默认跳过，避免日常单测在生产 Relay 中创建 route。
func TestLiveRelayDeployment(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("RELAY_LIVE_BASE_URL"), "/")
	token := os.Getenv("RELAY_LIVE_PROVISION_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("set RELAY_LIVE_BASE_URL and RELAY_LIVE_PROVISION_TOKEN for deployment acceptance")
	}
	response, data := requestJSON(t, http.MethodGet, baseURL+"/healthz", "", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("public health status=%d body=%s", response.StatusCode, data)
	}
	response, data = requestJSON(t, http.MethodPost, baseURL+"/v1/routes/register", token, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register live route status=%d body=%s", response.StatusCode, data)
	}
	var route struct {
		RouteID    string `json:"routeId"`
		BridgeAuth string `json:"bridgeAuth"`
	}
	if err := json.Unmarshal(data, &route); err != nil {
		t.Fatal(err)
	}
	deviceID := "deployment-" + time.Now().UTC().Format("20060102-150405")
	response, data = requestJSON(t, http.MethodPost, baseURL+"/v1/routes/"+route.RouteID+"/devices/register", route.BridgeAuth, map[string]string{"deviceId": deviceID})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register live device status=%d body=%s", response.StatusCode, data)
	}
	var device struct {
		DeviceAuth string `json:"deviceAuth"`
	}
	if err := json.Unmarshal(data, &device); err != nil {
		t.Fatal(err)
	}

	bridge := liveWSDial(t, baseURL, "/v1/routes/"+route.RouteID+"/bridge", route.BridgeAuth)
	phone := liveWSDial(t, baseURL, "/v1/routes/"+route.RouteID+"/devices/"+deviceID, device.DeviceAuth)
	online := []byte(`{"routeId":"` + route.RouteID + `","senderId":"bridge","destinationId":"` + deviceID + `","ciphertext":"public-online"}`)
	if err := bridge.WriteMessage(websocket.TextMessage, online); err != nil {
		t.Fatal(err)
	}
	_, delivered, err := phone.ReadMessage()
	if err != nil || string(delivered) != string(online) {
		t.Fatalf("online delivery payload=%s err=%v", delivered, err)
	}
	_ = phone.Close()

	offline := []byte(`{"routeId":"` + route.RouteID + `","senderId":"bridge","destinationId":"` + deviceID + `","keyEpochId":"mailbox:0","ciphertext":"public-offline"}`)
	if err := bridge.WriteMessage(websocket.TextMessage, offline); err != nil {
		t.Fatal(err)
	}
	var mailbox struct {
		Frames []MailboxFrame `json:"frames"`
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		response, data = requestJSON(t, http.MethodGet, baseURL+"/v1/routes/"+route.RouteID+"/devices/"+deviceID+"/mailbox", device.DeviceAuth, nil)
		if response.StatusCode == http.StatusOK {
			_ = json.Unmarshal(data, &mailbox)
			if len(mailbox.Frames) == 1 {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(mailbox.Frames) != 1 || string(mailbox.Frames[0].Envelope) != string(offline) {
		t.Fatalf("offline delivery body=%s", data)
	}
	if gate := os.Getenv("RELAY_LIVE_RESTART_GATE"); gate != "" {
		if err := os.WriteFile(gate, []byte("mailbox-persisted-before-restart"), 0o600); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(gate)
		for deadline := time.Now().Add(45 * time.Second); ; {
			if _, err := os.Stat(gate); os.IsNotExist(err) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for relay restart gate")
			}
			time.Sleep(100 * time.Millisecond)
		}
		mailbox.Frames = nil
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			response, data = requestJSON(t, http.MethodGet, baseURL+"/v1/routes/"+route.RouteID+"/devices/"+deviceID+"/mailbox", device.DeviceAuth, nil)
			if response.StatusCode == http.StatusOK {
				_ = json.Unmarshal(data, &mailbox)
				if len(mailbox.Frames) == 1 {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if len(mailbox.Frames) != 1 || string(mailbox.Frames[0].Envelope) != string(offline) {
			t.Fatalf("offline delivery did not survive relay restart body=%s", data)
		}
	}
	response, data = requestJSON(t, http.MethodPost, baseURL+"/v1/routes/"+route.RouteID+"/devices/"+deviceID+"/mailbox/ack", device.DeviceAuth, map[string]uint64{"through": mailbox.Frames[0].Cursor})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("live ack status=%d body=%s", response.StatusCode, data)
	}
	response, data = requestJSON(t, http.MethodPost, baseURL+"/v1/routes/"+route.RouteID+"/devices/"+deviceID+"/revoke", route.BridgeAuth, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("live revoke status=%d body=%s", response.StatusCode, data)
	}
}

func liveWSDial(t *testing.T, baseURL, path, auth string) *websocket.Conn {
	t.Helper()
	address, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	address.Scheme = "wss"
	address.Path = path
	header := http.Header{"Authorization": []string{"Bearer " + auth}}
	conn, response, err := websocket.DefaultDialer.Dial(address.String(), header)
	if err != nil {
		var status int
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("live websocket dial status=%d err=%v", status, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
