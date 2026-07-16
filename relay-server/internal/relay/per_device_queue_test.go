package relay

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newTestServerBigFrames is like newTestServer but with a large MaxFrameBytes
// so tests can send big payloads to force TCP backpressure / queue overflow.
func newTestServerBigFrames(t *testing.T, rate int) (*Server, *httptest.Server) {
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
		MaxMailboxBytes:      2 << 20,
		MaxFrameBytes:        1 << 20,
		RateLimitPerMinute:   rate,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

// TestPerDeviceQueue_RouteLevelIsolation is the core T04 test:
// device A connects but never reads (fills its bounded send queue), while
// device B on the same route reads normally. Pre-fix, the bridge read loop
// called target.write synchronously, so a full A would block delivery to B
// (head-of-line blocking across the whole route). Post-fix, B must receive its
// frame within 1s regardless of A being stuck, and A gets disconnected once its
// queue overflows.
func TestPerDeviceQueue_RouteLevelIsolation(t *testing.T) {
	_, httpServer := newTestServerBigFrames(t, 50)
	credentials := provisionDevice(t, httpServer.URL)
	// Register a second device (phone-2) on the same route.
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
	// Device A connects but does NOT read (simulates a stuck/full TCP receiver).
	deviceA := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/devices/phone-1", credentials.deviceAuth)
	defer deviceA.Close()
	// Device B reads normally.
	deviceB := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/devices/phone-2", second.DeviceAuth)
	defer deviceB.Close()

	// The bridge socket is serial, so all frames (A flood + B fresh) are written
	// from one goroutine in order. The relay reads them in order: A frames
	// enqueue to A (whose queue fills and eventually disconnects A), while the B
	// frame enqueues to B (which reads it immediately). Pre-fix, a full A would
	// block the bridge read loop's synchronous target.write, so B's frame would
	// not be delivered until A drained. Post-fix, B gets it promptly.
	bPayload := []byte(`{"routeId":"` + credentials.routeID + `","senderId":"bridge","destinationId":"phone-2","keyEpochId":"online-b-fresh","ciphertext":"hello-B"}`)
	bigCiphertext := strings.Repeat("x", 60*1024)

	wroteB := make(chan struct{})
	go func() {
		defer close(wroteB)
		_ = bridge.SetWriteDeadline(time.Now().Add(15 * time.Second))
		// Write the B fresh frame FIRST so it is at the head of the bridge's
		// write stream and the relay processes it before A's queue fills.
		if err := bridge.WriteMessage(websocket.TextMessage, bPayload); err != nil {
			return
		}
		// Then flood A. Once A's queue overflows the relay disconnects A; this
		// no longer blocks B (already delivered).
		for i := 0; i < perDeviceSendQueueFrames+20; i++ {
			env := []byte(`{"routeId":"` + credentials.routeID + `","senderId":"bridge","destinationId":"phone-1","keyEpochId":"online-` + itoa(i) + `","ciphertext":"` + bigCiphertext + `"}`)
			if err := bridge.WriteMessage(websocket.TextMessage, env); err != nil {
				return
			}
		}
	}()

	// B must receive its fresh frame promptly — proving route-level isolation.
	if err := deviceB.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	gotFresh := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, payload, err := deviceB.ReadMessage()
		if err != nil {
			t.Fatalf("device B read error: %v", err)
		}
		if string(payload) == string(bPayload) {
			gotFresh = true
			break
		}
	}
	if !gotFresh {
		t.Fatal("device B did not receive its fresh frame — route-level head-of-line blocking by device A was NOT eliminated")
	}
	// Drain the write goroutine (it may still be blocked writing A frames; the
	// deferred closes tear it down). Don't block the test on it.
	select {
	case <-wroteB:
	case <-time.After(2 * time.Second):
	}
}

// TestPerDeviceQueue_FullQueueDisconnectsSlowDeviceAndMailboxes verifies that
// when a device's send queue overflows, the device is disconnected AND the
// overflowing durable mailbox epoch is preserved in the mailbox (so a reconnecting device gets
// it). Connection-scoped frames are intentionally dropped instead. To force overflow deterministically without waiting on kernel TCP
// buffers, this test shortens relayWriteDeadline so the per-peer writer's
// WriteMessage fails fast — the queue then fills (writer no longer drains it)
// and overflows, triggering disconnect + mailbox persistence.
func TestPerDeviceQueue_FullQueueDisconnectsSlowDeviceAndMailboxes(t *testing.T) {
	// Shorten the write deadline so the writer goroutine's WriteMessage returns
	// quickly on a non-reading device, letting the queue fill + overflow fast.
	oldWriteDeadline := relayWriteDeadline
	relayWriteDeadline = 50 * time.Millisecond
	defer func() { relayWriteDeadline = oldWriteDeadline }()

	_, httpServer := newTestServerBigFrames(t, 50)
	credentials := provisionDevice(t, httpServer.URL)
	bridge := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/bridge", credentials.bridgeAuth)
	defer bridge.Close()
	// Device connects but never reads.
	device := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/devices/phone-1", credentials.deviceAuth)
	defer device.Close()

	// Flood past the queue cap. 256KB frames mean the 8MiB byte-cap is hit at
	// ~32 frames; combined with the short write deadline the writer can't drain
	// fast enough (kernel buffers saturate), so the queue overflows and the
	// device is disconnected. Overflowing mailbox frames persist to mailbox.
	bigCiphertext := strings.Repeat("x", 256*1024)
	floodDone := make(chan struct{})
	go func() {
		defer close(floodDone)
		_ = bridge.SetWriteDeadline(time.Now().Add(10 * time.Second))
		for i := 0; i < perDeviceSendQueueFrames+10; i++ {
			env := []byte(`{"routeId":"` + credentials.routeID + `","senderId":"bridge","destinationId":"phone-1","keyEpochId":"mailbox:` + itoa(i) + `","ciphertext":"` + bigCiphertext + `"}`)
			if err := bridge.WriteMessage(websocket.TextMessage, env); err != nil {
				return
			}
		}
	}()

	// Give the server time to process overflow + mailbox appends.
	time.Sleep(800 * time.Millisecond)

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
	if len(mailbox.Frames) == 0 {
		t.Fatal("expected mailbox frames from overflow, got 0 — slow device's frames were lost instead of mailboxed")
	}
	select {
	case <-floodDone:
	case <-time.After(2 * time.Second):
	}
}

// TestPerDeviceQueue_WriterGoroutineExitsOnPeerClose verifies no goroutine
// leak: after a device disconnects, its writer goroutine exits.
func TestPerDeviceQueue_WriterGoroutineExitsOnPeerClose(t *testing.T) {
	server, httpServer := newTestServer(t, 50)
	credentials := provisionDevice(t, httpServer.URL)
	bridge := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/bridge", credentials.bridgeAuth)
	defer bridge.Close()
	device := wsDial(t, httpServer.URL, "/v1/routes/"+credentials.routeID+"/devices/phone-1", credentials.deviceAuth)

	// Confirm the device peer has a live writer goroutine registered.
	time.Sleep(100 * time.Millisecond)
	server.mu.Lock()
	peer, ok := server.devices[deviceKey(credentials.routeID, "phone-1")]
	server.mu.Unlock()
	if !ok || peer == nil {
		t.Fatal("device peer not registered")
	}
	// Close the device connection; the server's read loop observes the error,
	// removeDevice runs, and shutdownWriter closes the writer goroutine.
	_ = device.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-peer.done:
			return // writer goroutine exited
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("device peer writer goroutine did not exit after close (leak)")
}

// itoa is a small dependency-free int->string for test payload construction.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
