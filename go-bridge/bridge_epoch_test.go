package gobridge

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

func TestGenerateBridgeEpochIsRandomUUID(t *testing.T) {
	first, err := generateBridgeEpoch()
	if err != nil {
		t.Fatalf("generate first epoch: %v", err)
	}
	second, err := generateBridgeEpoch()
	if err != nil {
		t.Fatalf("generate second epoch: %v", err)
	}
	if first == second {
		t.Fatalf("bridge epochs unexpectedly reused: %q", first)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(first) || !pattern.MatchString(second) {
		t.Fatalf("epochs are not UUIDv4 shaped: %q %q", first, second)
	}
}

func TestServerUsesOneInjectedEpochAcrossHelloAndRegister(t *testing.T) {
	const epoch = "11111111-2222-4333-8444-555555555555"
	server := httptest.NewServer(NewServerWithEpoch(NewHandlers(), epoch))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	helloConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial hello: %v", err)
	}
	defer helloConn.Close()
	if err := helloConn.WriteJSON(HelloMessage{
		Type:     "hello",
		Client:   HelloClient{App: "test", Version: "1", DeviceID: "device"},
		Protocol: HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	var helloAck HelloAckMessage
	if err := helloConn.ReadJSON(&helloAck); err != nil {
		t.Fatalf("read hello_ack: %v", err)
	}
	if helloAck.BridgeEpoch != epoch {
		t.Fatalf("hello epoch = %q, want %q", helloAck.BridgeEpoch, epoch)
	}

	registerConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial register: %v", err)
	}
	defer registerConn.Close()
	if err := registerConn.WriteJSON(map[string]any{"type": "register"}); err != nil {
		t.Fatalf("write register: %v", err)
	}
	var registerAck map[string]any
	if err := registerConn.ReadJSON(&registerAck); err != nil {
		t.Fatalf("read register_ack: %v", err)
	}
	if registerAck["bridgeEpoch"] != epoch {
		t.Fatalf("register epoch = %#v, want %q", registerAck["bridgeEpoch"], epoch)
	}
}

func TestServerInjectsProcessEpochIntoEventPublisher(t *testing.T) {
	const epoch = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	server := NewServerWithEpoch(NewHandlers(), epoch)
	if server.eventPublisher == nil {
		t.Fatal("server event publisher is nil")
	}
	if got := server.eventPublisher.BridgeEpoch(); got != epoch {
		t.Fatalf("event publisher epoch = %q, want %q", got, epoch)
	}
}

func TestConcurrentHelloConnectionsShareProcessEpoch(t *testing.T) {
	const epoch = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	server := httptest.NewServer(NewServerWithEpoch(NewHandlers(), epoch))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				errors <- err
				return
			}
			defer conn.Close()
			if err := conn.WriteJSON(HelloMessage{Type: "hello", Client: HelloClient{DeviceID: "device"}, Protocol: HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion}}); err != nil {
				errors <- err
				return
			}
			var ack HelloAckMessage
			if err := conn.ReadJSON(&ack); err != nil {
				errors <- err
				return
			}
			if ack.BridgeEpoch != epoch {
				errors <- &epochMismatchError{got: ack.BridgeEpoch, want: epoch}
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

type epochMismatchError struct{ got, want string }

func (e *epochMismatchError) Error() string {
	return "hello epoch mismatch: got " + e.got + ", want " + e.want
}
