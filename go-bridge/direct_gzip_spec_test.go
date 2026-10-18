package gobridge

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// direct_gzip_v1 spec tests（S-8，方案 v30 §3.6/§6；canonical =
// docs/protocol/bridge-v1.md §Direct Frame Compression — FROZEN SPEC + 冻结样本
// docs/protocol/samples/direct-gzip-v1/）。
//
// 覆盖：fixture roundtrip 与 README 哈希、协商 echo 语义（声明才 echo、失败 ack 不
// echo）、阈值边界（恰低于 32KiB → text / 恰达到 32KiB 且可压 → binary）、未协商
// 连接零 binary 帧（Mac→client 方向冻结断言）、未压缩路径与旧 WriteJSON 字节一致
// （含尾随换行）。
//
// 「压缩后不更小 → 维持 text」为 sender 防御分支：valid JSON（ASCII < 8 bit/char）
// 经 gzip 恒可压（对齐 relay_gzip_v1 测试口径——relay 侧同样只覆盖可压路径）。

func directGzipFixturePath(name string) string {
	return strings.Join([]string{"..", "docs", "protocol", "samples", "direct-gzip-v1", name}, string(os.PathSeparator))
}

func TestDirectGzipSpecFixtureRoundtripAndHashes(t *testing.T) {
	gzRaw, err := os.ReadFile(directGzipFixturePath("compressed-frame.json.gz"))
	if err != nil {
		t.Fatalf("read fixture gz: %v", err)
	}
	plainRaw, err := os.ReadFile(directGzipFixturePath("compressed-frame.decoded.json"))
	if err != nil {
		t.Fatalf("read fixture decoded: %v", err)
	}

	// README 登记的 SHA-256（冻结物漂移防护：改任一文件必须同步 README 与两仓副本）。
	gzSum := sha256.Sum256(gzRaw)
	if got, want := hex.EncodeToString(gzSum[:]), "09e4ce2baa96cba90d3f4c01609bcbf0209e7194c89e9a5f54fb2c07dc45ee27"; got != want {
		t.Fatalf("fixture gz sha256 drift: got %s want %s", got, want)
	}
	plainSum := sha256.Sum256(plainRaw)
	if got, want := hex.EncodeToString(plainSum[:]), "63371826332a8d4ce89d57b4f155809d1c9f7ce0b61f0242a542dd1df2c69925"; got != want {
		t.Fatalf("fixture decoded sha256 drift: got %s want %s", got, want)
	}

	// gunzip == decoded JSON（binary → gunzip → 与 text 路径同一形状）。
	zr, err := gzip.NewReader(bytes.NewReader(gzRaw))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	roundtrip, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Equal(roundtrip, plainRaw) {
		t.Fatalf("fixture roundtrip mismatch: %d vs %d bytes", len(roundtrip), len(plainRaw))
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(plainRaw, &envelope); err != nil {
		t.Fatalf("decoded fixture is not JSON object: %v", err)
	}
	if string(envelope["type"]) != `"result"` {
		t.Fatalf("fixture envelope type = %s, want result", envelope["type"])
	}
}

func TestDirectGzipNegotiationEchoSemantics(t *testing.T) {
	s := &Server{}
	conn := openTestConnServerSide(t)

	// 未声明 → 不 echo。
	ack := newHelloAckForDirectGzipTest()
	s.negotiateDirectGzipV1(ack, &HelloMessage{Capabilities: []string{"session_sync_v2"}}, conn)
	if ack.Capabilities["direct_gzip_v1"] {
		t.Fatalf("undeclared client must not get direct_gzip_v1 echo")
	}

	// 声明 + ack.Ok → echo。
	ack = newHelloAckForDirectGzipTest()
	s.negotiateDirectGzipV1(ack, &HelloMessage{Capabilities: []string{"direct_gzip_v1"}}, conn)
	if !ack.Capabilities["direct_gzip_v1"] {
		t.Fatalf("declared client with ok ack must get direct_gzip_v1 echo")
	}

	// 声明但 ack 失败 → 不 echo（hello 失败路径无 capability）。
	failed := newHelloAckForDirectGzipTest()
	failed.Ok = false
	s.negotiateDirectGzipV1(failed, &HelloMessage{Capabilities: []string{"direct_gzip_v1"}}, conn)
	if failed.Capabilities["direct_gzip_v1"] {
		t.Fatalf("failed ack must not echo direct_gzip_v1")
	}
}

func TestDirectGzipSendThresholdBoundaryAndOpcode(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	defer clientConn.Close()
	defer serverConn.Close()

	serverConn.SetDirectGzipEnabled(true)

	// ① 小帧 → text，不压缩；字节与旧 WriteJSON 路径一致（含尾随换行）。
	small := map[string]interface{}{"type": "ping", "n": 1}
	if err := serverConn.SendJSONReport(small); err != nil {
		t.Fatalf("send small: %v", err)
	}
	mt, payload, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("read small: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("small frame opcode = %d, want text", mt)
	}
	if want := mustEncodeWithTrailingNewlineForTest(t, small); !bytes.Equal(payload, want) {
		t.Fatalf("small frame bytes drifted from WriteJSON-compatible encoding")
	}

	// ② 恰低于阈值（序列化 < 32 KiB）→ text。
	justUnder := map[string]interface{}{"type": "result", "data": compressibleStringForDirectGzipTest(directGzipThreshold-512)}
	if got := len(mustEncodeWithTrailingNewlineForTest(t, justUnder)); got >= directGzipThreshold {
		t.Fatalf("test setup: just-under frame serialized to %d bytes (>= threshold)", got)
	}
	if err := serverConn.SendJSONReport(justUnder); err != nil {
		t.Fatalf("send just-under: %v", err)
	}
	if mt, _, err := clientConn.ReadMessage(); err != nil {
		t.Fatalf("read just-under: %v", err)
	} else if mt != websocket.TextMessage {
		t.Fatalf("just-under-threshold frame opcode = %d, want text", mt)
	}

	// ③ 恰达到阈值且可压（序列化 ≥ 32 KiB）→ binary；gunzip 后逐字节等于原 JSON。
	justOver := map[string]interface{}{"type": "result", "requestId": "req_gz", "data": compressibleStringForDirectGzipTest(directGzipThreshold)}
	if err := serverConn.SendJSONReport(justOver); err != nil {
		t.Fatalf("send just-over: %v", err)
	}
	mt, payload, err = clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("read just-over: %v", err)
	}
	if mt != websocket.BinaryMessage {
		t.Fatalf("compressible over-threshold frame opcode = %d, want binary", mt)
	}
	want := mustEncodeWithTrailingNewlineForTest(t, justOver)
	got, err := gunzipBytesForTest(t, payload)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("compressed frame roundtrip mismatch: %d vs %d bytes", len(got), len(want))
	}
	if len(payload) >= len(want) {
		t.Fatalf("compressed payload %d bytes not smaller than raw %d bytes", len(payload), len(want))
	}
}

func TestDirectGzipUnnegotiatedConnZeroBinaryFrames(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	defer clientConn.Close()
	defer serverConn.Close()

	// 不调用 SetDirectGzipEnabled（= 未协商连接）。
	big := map[string]interface{}{"type": "result", "data": compressibleStringForDirectGzipTest(directGzipThreshold * 2)}
	if err := serverConn.SendJSONReport(big); err != nil {
		t.Fatalf("send: %v", err)
	}
	mt, _, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 冻结断言（限 Mac→client 方向）：未协商连接零 binary 帧。
	if mt != websocket.TextMessage {
		t.Fatalf("un-negotiated connection received binary frame (opcode %d) — violates §Direct Frame Compression", mt)
	}
}

// --- helpers ---

func newHelloAckForDirectGzipTest() *HelloAckMessage {
	return &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
}

// openTestConnServerSide 复用 openTestConn 的服务端连接（协商语义测试只需要 *Conn）。
func openTestConnServerSide(t *testing.T) *Conn {
	t.Helper()
	conn, _, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	return conn
}

func mustEncodeWithTrailingNewlineForTest(t *testing.T, v interface{}) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func gunzipBytesForTest(t *testing.T, payload []byte) ([]byte, error) {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

// compressibleStringForDirectGzipTest 生成指定长度的重复模式字符串（确定性可压）。
func compressibleStringForDirectGzipTest(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte(byte('a' + i%26))
	}
	return sb.String()
}
