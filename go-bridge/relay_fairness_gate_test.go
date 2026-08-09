package gobridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/go-bridge/relaystate"
)

type deterministicRelayProxy struct {
	bandwidthBytesPerSecond int
	baseDelay               time.Duration
	jitter                  []time.Duration
	stallWrite              int64
	stallDuration           time.Duration
	cutWrite                int64

	writes       atomic.Int64
	chunkWrites  atomic.Int64
	firstChunk   chan struct{}
	firstOnce    sync.Once
	bulkComplete chan struct{}
	completeOnce sync.Once
}

func (p *deterministicRelayProxy) send(raw json.RawMessage) error {
	writeNumber := p.writes.Add(1)
	var envelope RelayEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if envelope.Chunk != nil {
		p.chunkWrites.Add(1)
		p.firstOnce.Do(func() { close(p.firstChunk) })
		if envelope.Chunk.Index+1 == envelope.Chunk.Count && p.bulkComplete != nil {
			p.completeOnce.Do(func() { close(p.bulkComplete) })
		}
	}
	if p.cutWrite > 0 && writeNumber == p.cutWrite {
		return errors.New("deterministic connection cut")
	}
	delay := p.baseDelay
	if p.bandwidthBytesPerSecond > 0 {
		delay += time.Duration(int64(time.Second) * int64(len(raw)) / int64(p.bandwidthBytesPerSecond))
	}
	if len(p.jitter) > 0 {
		delay += p.jitter[(writeNumber-1)%int64(len(p.jitter))]
	}
	if writeNumber == p.stallWrite {
		delay += p.stallDuration
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	return nil
}

func deterministicHighEntropyBytes(size int) []byte {
	result := make([]byte, size)
	state := uint64(0x9e3779b97f4a7c15)
	for index := range result {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		result[index] = byte(state)
	}
	return result
}

func newFairnessGateConn(t *testing.T, proxy *deterministicRelayProxy) (*RelayDeviceConn, *relayOutboundWriter) {
	t.Helper()
	key := make([]byte, 32)
	conn := NewRelayDeviceConn("fairness-device", "bridge", "route", 1, nil, key, nil, proxy.send)
	writer := newRelayOutboundWriter()
	conn.setOutboundWriter(writer)
	t.Cleanup(writer.close)
	return conn, writer
}

func admitFairnessBulk(t *testing.T, conn *RelayDeviceConn, writer *relayOutboundWriter, payload []byte) {
	t.Helper()
	count := uint32((len(payload) + relayChunkTargetBytes - 1) / relayChunkTargetBytes)
	handle := newOutboundBulkHandle("fairness-group")
	job := &relayOutboundJob{
		conn:    conn,
		payload: payload,
		cursor: &relayChunkCursor{
			groupID: "fairness-group", count: count, chunkBytes: relayChunkTargetBytes,
			handle: handle, channelGeneration: conn.channelGeneration(),
			expiresAt: time.Now().Add(relayBulkCursorMaxAge),
		},
	}
	if err := writer.admitBulk(job); err != nil {
		t.Fatalf("admit 2 MiB bulk: %v", err)
	}
}

func TestRelayFairnessGateInteractiveLatencyDuringTwoMiBBulk(t *testing.T) {
	proxy := &deterministicRelayProxy{
		bandwidthBytesPerSecond: 8 << 20,
		baseDelay:               2 * time.Millisecond,
		jitter:                  []time.Duration{0, time.Millisecond, 2 * time.Millisecond, time.Millisecond},
		stallWrite:              4,
		stallDuration:           20 * time.Millisecond,
		firstChunk:              make(chan struct{}),
		bulkComplete:            make(chan struct{}),
	}
	conn, writer := newFairnessGateConn(t, proxy)
	admitFairnessBulk(t, conn, writer, deterministicHighEntropyBytes(2<<20))

	select {
	case <-proxy.firstChunk:
	case <-time.After(time.Second):
		t.Fatal("2 MiB bulk did not start")
	}

	latencies := make([]time.Duration, 0, 20)
	for index := 0; index < 20; index++ {
		event := "text_delta"
		if index%2 == 1 {
			event = "permission_asked"
		}
		payload, err := json.Marshal(map[string]any{
			"type": "event", "event": event, "data": map[string]any{"sequence": index},
		})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if err := writer.enqueue(&relayOutboundJob{conn: conn, payload: payload, class: relayOutboundInteractive}); err != nil {
			t.Fatalf("interactive %d: %v", index, err)
		}
		latencies = append(latencies, time.Since(started))
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[(len(latencies)*95+99)/100-1]
	maximum := latencies[len(latencies)-1]
	t.Logf("deterministic Relay fairness: samples=%d p95=%s max=%s bandwidth=%dB/s base=%s stall=%s", len(latencies), p95, maximum, proxy.bandwidthBytesPerSecond, proxy.baseDelay, proxy.stallDuration)
	if p95 > 100*time.Millisecond || maximum > 150*time.Millisecond {
		t.Fatalf("interactive latency gate failed: p95=%s/100ms max=%s/150ms", p95, maximum)
	}
	if proxy.chunkWrites.Load() < 2 {
		t.Fatalf("bulk was not interleaved: chunk writes=%d", proxy.chunkWrites.Load())
	}
	select {
	case <-proxy.bulkComplete:
	case <-time.After(2 * time.Second):
		t.Fatal("2 MiB bulk did not complete within deterministic proxy budget")
	}
}

func TestRelayFairnessGateConnectionCutClosesGeneration(t *testing.T) {
	proxy := &deterministicRelayProxy{
		bandwidthBytesPerSecond: 8 << 20,
		baseDelay:               time.Millisecond,
		cutWrite:                3,
		firstChunk:              make(chan struct{}),
	}
	conn, writer := newFairnessGateConn(t, proxy)
	admitFairnessBulk(t, conn, writer, deterministicHighEntropyBytes(2<<20))

	deadline := time.Now().Add(time.Second)
	for !conn.isClosed() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !conn.isClosed() {
		t.Fatal("deterministic connection cut did not close Relay generation")
	}
}

func TestRelayFairnessGateGzipAndDigest(t *testing.T) {
	repetitive := bytes.Repeat([]byte("cordcode-relay-fairness\n"), (2<<20)/24)
	effective, err := gzipPayload(repetitive)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective) >= len(repetitive)/10 {
		t.Fatalf("effective gzip ratio too weak: compressed=%d raw=%d", len(effective), len(repetitive))
	}

	ineffectiveRaw := deterministicHighEntropyBytes(2 << 20)
	ineffective, err := gzipPayload(ineffectiveRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(ineffective) < len(ineffectiveRaw) {
		t.Fatalf("high-entropy payload unexpectedly considered gzip-effective: compressed=%d raw=%d", len(ineffective), len(ineffectiveRaw))
	}

	content := base64.RawStdEncoding.EncodeToString(ineffectiveRaw)
	directPayload, err := json.Marshal(map[string]any{
		"type": "result", "requestId": "digest", "ok": true,
		"data": map[string]any{"kind": "text", "content": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, out := newReadFileBulkTestConn(t, true, false)
	conn.mu.Lock()
	key := append([]byte(nil), conn.macToIosKey...)
	conn.mu.Unlock()
	conn.registerRequestClass("digest", "read_file_v2", "")
	conn.SendResult("digest", map[string]any{"kind": "text", "content": content}, nil)
	relayPayload, sawChunk, encoding := collectResultFrames(t, out, key)
	if !sawChunk || encoding != "" {
		t.Fatalf("digest fixture did not use uncompressed Relay chunks: chunk=%v encoding=%q", sawChunk, encoding)
	}
	directDigest := sha256.Sum256(directPayload)
	relayDigest := sha256.Sum256(relayPayload)
	if relayDigest != directDigest {
		t.Fatalf("direct/Relay digest mismatch: direct=%x relay=%x", directDigest, relayDigest)
	}
}

func TestRelayFairnessGateDeadlineAlignment(t *testing.T) {
	if relayBulkCursorMaxAge != relaystate.ServerGroupMaxAge {
		t.Fatalf("writer cursor=%s relaystate server max=%s", relayBulkCursorMaxAge, relaystate.ServerGroupMaxAge)
	}
	if relayBulkCursorMaxAge <= relaystate.ClientTotalCap {
		t.Fatalf("server cursor=%s must exceed client total=%s", relayBulkCursorMaxAge, relaystate.ClientTotalCap)
	}
}
