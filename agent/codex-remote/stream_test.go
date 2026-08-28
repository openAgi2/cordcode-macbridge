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
