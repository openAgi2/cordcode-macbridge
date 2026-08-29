package codexremote

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSubscribeCursorHeaderFailClosed(t *testing.T) {
	if _, _, ok := SubscribeCursorHeader(""); ok {
		t.Fatal("empty cursor must not populate x-codex-subscribe-cursor")
	}
	name, value, ok := SubscribeCursorHeader("real-cursor")
	if !ok || name != "x-codex-subscribe-cursor" || value != "real-cursor" {
		t.Fatalf("got %s=%q ok=%v", name, value, ok)
	}
}

func TestStreamWrapsAndUnwrapsJSONRPC(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()

	done := make(chan Envelope, 1)
	go func() {
		env, err := hostConn.Read()
		if err != nil {
			t.Errorf("host read: %v", err)
			return
		}
		done <- env
		seq := uint64(1)
		_ = hostConn.Write(Envelope{
			Type:     typeServerMessage,
			ClientID: env.ClientID,
			EnvID:    env.EnvID,
			StreamID: env.StreamID,
			SeqID:    &seq,
			Message:  json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`),
		})
	}()

	if err := stream.Send([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case env := <-done:
		if env.Type != typeClientMessage || env.EnvID != "env_desktop" || env.StreamID != "stream_primary" {
			t.Fatalf("client envelope = %+v", env)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for host envelope")
	}
	payload, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"ok":true`) {
		t.Fatalf("payload = %s", payload)
	}
	if stream.RecordedCursor() != "" {
		t.Fatal("must not invent a reconnect cursor")
	}
}

func TestStreamMismatchDisconnects(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	seq := uint64(1)
	_ = hostConn.Write(Envelope{
		Type:     typeServerMessage,
		ClientID: "client_probe",
		EnvID:    "env_other",
		StreamID: "stream_primary",
		SeqID:    &seq,
		Message:  json.RawMessage(`{"jsonrpc":"2.0","method":"x"}`),
	})
	if _, err := stream.Recv(); err == nil {
		t.Fatal("expected env mismatch to close the stream")
	}
}

func TestStreamReassemblesChunks(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	body := []byte(`{"jsonrpc":"2.0","id":7,"result":{"hello":"chunked"}}`)
	seq := uint64(9)
	count := 2
	size := len(body)
	mid := len(body) / 2
	parts := [][]byte{body[:mid], body[mid:]}
	for i, part := range parts {
		seg := i
		_ = hostConn.Write(Envelope{
			Type:               typeServerMessageChunk,
			ClientID:           "client_probe",
			EnvID:              "env_desktop",
			StreamID:           "stream_primary",
			SeqID:              &seq,
			SegmentID:          &seg,
			SegmentCount:       &count,
			MessageSizeBytes:   &size,
			MessageChunkBase64: encodeChunk(part),
		})
	}
	payload, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != string(body) {
		t.Fatalf("got %s", payload)
	}
}

func TestStreamRecordsCursorWithoutUsingItAsJSONRPC(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	seq := uint64(3)
	cur := "cursor-from-envelope"
	_ = hostConn.Write(Envelope{
		Type:     typeServerMessage,
		ClientID: "client_probe",
		EnvID:    "env_desktop",
		StreamID: "stream_primary",
		SeqID:    &seq,
		Cursor:   &cur,
		Message:  json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`),
	})
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	if stream.RecordedCursor() != cur {
		t.Fatalf("cursor = %q", stream.RecordedCursor())
	}
}

// IdleFor tracks app-server evidence, not relay ACKs. Upstream ClientTracker
// uses PongStatus::Active to prove the exact (client_id, stream_id) is alive.
func TestStreamIdleForTracksHostActivity(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_probe")
	defer stream.Close()
	if idle := stream.IdleFor(); idle > time.Second {
		t.Fatalf("fresh stream idle = %s", idle)
	}

	stream.mu.Lock()
	stream.lastHostActivity = time.Now().Add(-time.Hour)
	stream.mu.Unlock()
	if idle := stream.IdleFor(); idle < 59*time.Minute {
		t.Fatalf("idle = %s, want ~1h", idle)
	}

	seq := uint64(1)
	_ = hostConn.Write(Envelope{Type: typeAck, ClientID: "client_probe", EnvID: "env_desktop", StreamID: "stream_probe", SeqID: &seq})
	time.Sleep(50 * time.Millisecond)
	if idle := stream.IdleFor(); idle < 59*time.Minute {
		t.Fatalf("relay ACK must not refresh host activity, idle = %s", idle)
	}

	_ = hostConn.Write(Envelope{Type: typePong, ClientID: "client_probe", EnvID: "env_desktop", StreamID: "stream_probe", Status: "active"})
	deadline := time.After(2 * time.Second)
	for {
		if idle := stream.IdleFor(); idle < time.Second {
			return
		}
		select {
		case <-deadline:
			t.Fatal("active pong must reset the host-activity clock")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// pong status 非 active 表示中继存活但 Desktop 端点已消失：流必须判死，
// 否则 RPC 请求全部超时却无人重连（真机 2026-08-29 10:34 pong=unknown）。
func TestStreamFailsOnDetachedPong(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	defer hostConn.Close()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_probe")

	_ = hostConn.Write(Envelope{Type: typePong, ClientID: "client_probe", EnvID: "env_desktop", StreamID: "stream_probe", Status: "unknown"})
	deadline := time.After(2 * time.Second)
	for {
		if err := stream.Send([]byte("{}")); err != nil {
			return // stream is dead as required
		}
		select {
		case <-deadline:
			t.Fatal("pong status=unknown must fail the stream")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// active/空 pong 是健康信号，不得判死。
func TestStreamSurvivesActivePong(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	defer hostConn.Close()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_probe")
	defer stream.Close()

	_ = hostConn.Write(Envelope{Type: typePong, ClientID: "client_probe", EnvID: "env_desktop", StreamID: "stream_probe", Status: "active"})
	time.Sleep(100 * time.Millisecond)
	if err := stream.Send([]byte("{}")); err != nil {
		t.Fatalf("active pong must not fail the stream: %v", err)
	}
}

// A late response from an old stream must neither kill the new stream nor cross
// its epoch boundary. Request ids restart at 1, so delivery would create a
// false initialize success.
func TestStreamDropsStaleStreamIDOfSameClientEnv(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	defer hostConn.Close()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_new")
	defer stream.Close()

	seq := uint64(1)
	_ = hostConn.Write(Envelope{
		Type:     typeServerMessage,
		ClientID: "client_probe", EnvID: "env_desktop", StreamID: "stream_old",
		SeqID:   &seq,
		Message: json.RawMessage(`{"jsonrpc":"2.0","method":"turn/started","params":{}}`),
	})
	_ = hostConn.Write(Envelope{
		Type:     typeServerMessage,
		ClientID: "client_probe", EnvID: "env_desktop", StreamID: "stream_new",
		SeqID:   &seq,
		Message: json.RawMessage(`{"jsonrpc":"2.0","method":"turn/completed","params":{}}`),
	})
	payload, err := stream.Recv()
	if err != nil {
		t.Fatalf("current-stream envelope must be delivered, got error: %v", err)
	}
	if strings.Contains(string(payload), "turn/started") || !strings.Contains(string(payload), "turn/completed") {
		t.Fatalf("stale payload crossed epoch boundary: %s", payload)
	}
	if err := stream.Send([]byte("{}")); err != nil {
		t.Fatalf("stream must stay alive after stale envelope: %v", err)
	}
}

// client_id 或 env_id 不匹配的封包仍须判死流（外来流量不得混入）。
func TestStreamStillFailsOnForeignRouting(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	defer hostConn.Close()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_new")

	seq := uint64(1)
	_ = hostConn.Write(Envelope{
		Type:     typeServerMessage,
		ClientID: "client_other", EnvID: "env_desktop", StreamID: "stream_other",
		SeqID:   &seq,
		Message: json.RawMessage(`{}`),
	})
	deadline := time.After(2 * time.Second)
	for {
		if err := stream.Send([]byte("{}")); err != nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("foreign client_id must fail the stream")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
