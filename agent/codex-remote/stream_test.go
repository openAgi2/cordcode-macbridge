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

// 2026-09-14 ack 风暴：Desktop 0.154.0-alpha.6.2 对每条客户端 ack 回一条
// 哨兵 error 通知；若无断路，ack→error→ack 在 relay RTT 上自激振荡
// （实测 ~750 通知/s、2.8MB/s）。断路器语义：哨兵错误到达后，本连接
// 不再回 ack；普通错误（无哨兵）不受影响。
func TestStreamAckCircuitBreakerOnTransportSentinel(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	defer hostConn.Close()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_probe")
	defer stream.Close()

	writeMsg := func(seq uint64, payload string) {
		t.Helper()
		if err := hostConn.Write(Envelope{
			Type:     typeServerMessage,
			ClientID: "client_probe", EnvID: "env_desktop", StreamID: "stream_probe",
			SeqID:   &seq,
			Message: json.RawMessage(payload),
		}); err != nil {
			t.Fatalf("host write seq=%d: %v", seq, err)
		}
	}

	// 采集 client→host 的 ack（hostConn.Read 阻塞，交给后台 goroutine）。
	type hostFrame struct {
		env Envelope
		err error
	}
	frames := make(chan hostFrame, 16)
	go func() {
		for {
			env, err := hostConn.Read()
			frames <- hostFrame{env: env, err: err}
			if err != nil {
				return
			}
		}
	}()

	// 1. 普通通知：断路器未武装，应回 ack（这就是点燃服务器错误的那条）。
	writeMsg(1, `{"jsonrpc":"2.0","method":"remoteControl/status/changed","params":{}}`)
	// 2. 服务器对 ack 的拒绝（哨兵 threadId）。
	writeMsg(2, `{"jsonrpc":"2.0","method":"error","params":{"error":{"message":"Unexpected ack message received from client"},"willRetry":false,"threadId":"__remote_control_transport__","turnId":"__remote_control_transport__"}}`)
	// 3. 后续任何通知都不得再 ack。
	writeMsg(3, `{"jsonrpc":"2.0","method":"item/started","params":{"threadId":"th"}}`)

	var ackedSeqs []uint64
	deadline := time.After(1200 * time.Millisecond)
collect:
	for {
		select {
		case f := <-frames:
			if f.err != nil {
				break collect
			}
			if f.env.Type == typeAck && f.env.SeqID != nil {
				ackedSeqs = append(ackedSeqs, *f.env.SeqID)
			}
		case <-deadline:
			break collect
		}
	}
	if len(ackedSeqs) != 1 || ackedSeqs[0] != 1 {
		t.Fatalf("acks after sentinel breaker = %v, want exactly [1]", ackedSeqs)
	}
	if !stream.AcksDisabled() {
		t.Fatal("breaker must be armed after sentinel error")
	}
}

// 无哨兵的普通 error 通知照常 ack（断路器不过度匹配）。
func TestStreamStillAcksOrdinaryErrors(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	defer hostConn.Close()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_probe2")
	defer stream.Close()

	seq := uint64(7)
	if err := hostConn.Write(Envelope{
		Type:     typeServerMessage,
		ClientID: "client_probe", EnvID: "env_desktop", StreamID: "stream_probe2",
		SeqID:   &seq,
		Message: json.RawMessage(`{"jsonrpc":"2.0","method":"error","params":{"error":{"message":"boom"},"willRetry":false,"threadId":"th","turnId":"t"}}`),
	}); err != nil {
		t.Fatal(err)
	}
	env, err := hostConn.Read()
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != typeAck || env.SeqID == nil || *env.SeqID != 7 {
		t.Fatalf("ordinary error ack = %+v", env)
	}
	if stream.AcksDisabled() {
		t.Fatal("ordinary error must not arm the breaker")
	}
}
