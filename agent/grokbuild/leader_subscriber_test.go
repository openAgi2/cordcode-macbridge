package grokbuild

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Test helpers for the mock leader server side (mirror the client frame codec).

func writeTestFrame(w io.Writer, payload []byte) error {
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(payload)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readTestFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// writeACPResponse wraps a JSON-RPC response in a leader "acp" envelope and frames it.
func writeACPResponse(w io.Writer, id int, result any) error {
	rj, _ := json.Marshal(result)
	resp := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPC: "2.0", ID: id, Result: rj}
	b, _ := json.Marshal(resp)
	env, _ := json.Marshal(leaderServerMsg{Type: "acp", Payload: string(b)})
	return writeTestFrame(w, env)
}

// writeACPNotification wraps a JSON-RPC notification in a leader "acp" envelope and frames it.
func writeACPNotification(w io.Writer, method string, params any) error {
	pj, _ := json.Marshal(params)
	notif := struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{JSONRPC: "2.0", Method: method, Params: pj}
	b, _ := json.Marshal(notif)
	env, _ := json.Marshal(leaderServerMsg{Type: "acp", Payload: string(b)})
	return writeTestFrame(w, env)
}

// acpPayloadID extracts the JSON-RPC id from an "acp" envelope payload string.
func acpPayloadID(payload string) int {
	var probe struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal([]byte(payload), &probe)
	return probe.ID
}

func readClientMsg(r io.Reader) (leaderClientMsg, error) {
	frame, err := readTestFrame(r)
	if err != nil {
		return leaderClientMsg{}, err
	}
	var m leaderClientMsg
	return m, json.Unmarshal(frame, &m)
}

// TestLeaderSubscriberReceivesLiveSessionUpdate verifies the full handshake
// (register → initialize → session/load) and that live session/update notifications
// are converted to core.Events via convertSessionUpdate, while replay updates are dropped.
func TestLeaderSubscriberReceivesLiveSessionUpdate(t *testing.T) {
	// macOS sun_path limit is 104 bytes; t.TempDir() under /var/folders is too long.
	sock := filepath.Join("/tmp", fmt.Sprintf("cc-grok-leader-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var got []core.Event
	var mu sync.Mutex
	onEvent := func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}

	serverErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()

		reg, err := readClientMsg(c)
		if err != nil {
			serverErr <- err
			return
		}
		if reg.Type != "register" || reg.ClientType != leaderClientType {
			serverErr <- fmt.Errorf("want register/%s, got %s/%s", leaderClientType, reg.Type, reg.ClientType)
			return
		}
		rr, _ := json.Marshal(leaderServerMsg{Type: "registered", Ready: true})
		if err := writeTestFrame(c, rr); err != nil {
			serverErr <- err
			return
		}

		init, err := readClientMsg(c)
		if err != nil {
			serverErr <- err
			return
		}
		if init.Type != "acp" {
			serverErr <- fmt.Errorf("want acp initialize, got %s", init.Type)
			return
		}
		if err := writeACPResponse(c, acpPayloadID(init.Payload), map[string]any{"protocolVersion": "1"}); err != nil {
			serverErr <- err
			return
		}

		load, err := readClientMsg(c)
		if err != nil {
			serverErr <- err
			return
		}
		// Replay update before the load response — must be dropped.
		if err := writeACPNotification(c, "session/update", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "REPLAY-DROP"}},
			"_meta":     map[string]any{"isReplay": true},
		}); err != nil {
			serverErr <- err
			return
		}
		if err := writeACPResponse(c, acpPayloadID(load.Payload), map[string]any{}); err != nil {
			serverErr <- err
			return
		}

		// Live updates (no isReplay) — must be forwarded through convertSessionUpdate.
		if err := writeACPNotification(c, "session/update", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "hello leader"}},
		}); err != nil {
			serverErr <- err
			return
		}
		if err := writeACPNotification(c, "session/update", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "planning"}},
		}); err != nil {
			serverErr <- err
			return
		}
		// Give the subscriber a moment to process, then close so Run returns.
		time.Sleep(150 * time.Millisecond)
		serverErr <- nil
	}()

	sub := NewLeaderSubscriber(sock, "sess-1", "/tmp")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = sub.Run(ctx, onEvent) // returns on conn close (or ctx safety net)

	if err := <-serverErr; err != nil {
		t.Fatalf("mock leader server: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (replay dropped): %+v", len(got), got)
	}
	if got[0].Type != core.EventText || got[0].Content != "hello leader" {
		t.Fatalf("got[0] = %+v, want EventText 'hello leader'", got[0])
	}
	if got[1].Type != core.EventThinking || got[1].Content != "planning" {
		t.Fatalf("got[1] = %+v, want EventThinking 'planning'", got[1])
	}
}

// TestLeaderSubscriberReturnsErrorWhenSocketMissing: no leader running → fail fast,
// do NOT spawn or hang.
func TestLeaderSubscriberReturnsErrorWhenSocketMissing(t *testing.T) {
	sub := NewLeaderSubscriber(filepath.Join(t.TempDir(), "missing.sock"), "sess-1", "/tmp")
	err := sub.Run(context.Background(), func(core.Event) {})
	if err == nil {
		t.Fatal("want error dialing missing socket, got nil")
	}
}
