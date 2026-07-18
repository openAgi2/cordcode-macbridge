package gobridge

import (
	"bytes"
	"compress/gzip"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestRelayCryptoIdentityKeyPersistence 验证密钥生成后可重新加载。
func TestRelayCryptoIdentityKeyPersistence(t *testing.T) {
	dir := t.TempDir()
	ci1, err := LoadOrCreateRelayCryptoIdentity(dir)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if ci1.PublicKeyBytes() == nil {
		t.Fatal("public key should not be nil")
	}
	fp1 := ci1.Fingerprint()
	if fp1 == "" {
		t.Fatal("fingerprint should not be empty")
	}

	// 再次加载应得到相同密钥
	ci2, err := LoadOrCreateRelayCryptoIdentity(dir)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	fp2 := ci2.Fingerprint()
	if fp1 != fp2 {
		t.Fatalf("fingerprint mismatch: %s != %s", fp1, fp2)
	}
	if string(ci1.PublicKeyBytes()) != string(ci2.PublicKeyBytes()) {
		t.Fatal("public key bytes should match")
	}
}

// TestRelayCryptoIdentityFingerprintStable 验证 fingerprint 在多次调用间稳定。
func TestRelayCryptoIdentityFingerprintStable(t *testing.T) {
	dir := t.TempDir()
	ci, _ := LoadOrCreateRelayCryptoIdentity(dir)

	fp1 := ci.Fingerprint()
	fp2 := ci.Fingerprint()
	if fp1 != fp2 {
		t.Fatalf("fingerprint should be stable: %s != %s", fp1, fp2)
	}
}

// TestOnlineHandshakeDerivesTrafficKeys 验证 ECDHE 握手完整流程。
func TestOnlineHandshakeDerivesTrafficKeys(t *testing.T) {
	// 生成双方长期 identity
	macPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	iosPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	bridgeID := "bridge-test"
	deviceID := "device-test"

	// Mac 派生 identity auth key
	shared, err := macPriv.ECDH(iosPriv.PublicKey())
	if err != nil {
		t.Fatalf("identity ECDH: %v", err)
	}
	context, _ := json.Marshal([]string{bridgeID, deviceID})
	identityAuthKey, err := hkdfExpand(shared, append([]byte(identityAuthLabel), context...), 32)
	if err != nil {
		t.Fatalf("HKDF identity auth: %v", err)
	}

	// Mac 创建握手
	hs, err := NewOnlineHandshake(identityAuthKey)
	if err != nil {
		t.Fatalf("new handshake: %v", err)
	}

	// iOS 构造 client_hello
	iosEphPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	iosEphPub := iosEphPriv.PublicKey().Bytes()
	clientRandom := make([]byte, 32)
	rand.Read(clientRandom)

	// Mac 处理 client hello
	hello := signedOnlineClientHello(t, identityAuthKey, bridgeID, deviceID, 1, iosEphPub, clientRandom)
	response, err := hs.AcceptClientHello(hello)
	if err != nil {
		t.Fatalf("process client hello: %v", err)
	}

	if !hs.Completed() {
		t.Fatal("handshake should be completed")
	}

	macKey := hs.MacToIosKey()
	iosKey := hs.IosToMacKey()
	if len(macKey) != 32 {
		t.Fatalf("mac-to-ios key length = %d, want 32", len(macKey))
	}
	if len(iosKey) != 32 {
		t.Fatalf("ios-to-mac key length = %d, want 32", len(iosKey))
	}

	// 两个方向密钥应不同
	if hmac.Equal(macKey, iosKey) {
		t.Fatal("direction keys should differ")
	}

	serverHello, err := canonicalOnlineServerHello(*response)
	if err != nil || len(serverHello) == 0 {
		t.Fatal("server hello should not be empty")
	}
	if response.AuthTag == "" {
		t.Fatal("server auth tag should not be empty")
	}
}

// TestOnlineHandshakeRejectsBadAuthTag 验证错误 auth tag 被拒绝。
func TestOnlineHandshakeRejectsBadAuthTag(t *testing.T) {
	macPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	iosPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	shared, _ := macPriv.ECDH(iosPriv.PublicKey())
	context, _ := json.Marshal([]string{"b", "d"})
	identityAuthKey, _ := hkdfExpand(shared, append([]byte(identityAuthLabel), context...), 32)

	hs, _ := NewOnlineHandshake(identityAuthKey)

	iosEphPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientRandom := make([]byte, 32)
	rand.Read(clientRandom)
	hello := signedOnlineClientHello(t, identityAuthKey, "b", "d", 1, iosEphPriv.PublicKey().Bytes(), clientRandom)
	hello.AuthTag = base64.StdEncoding.EncodeToString(make([]byte, 32))
	_, err := hs.AcceptClientHello(hello)
	if err == nil {
		t.Fatal("should reject bad auth tag")
	}
}

// TestOnlineHandshakeDestroyClearsKeys 验证 Destroy 擦除密钥。
func TestOnlineHandshakeDestroyClearsKeys(t *testing.T) {
	macPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	iosPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	shared, _ := macPriv.ECDH(iosPriv.PublicKey())
	context, _ := json.Marshal([]string{"b", "d"})
	identityAuthKey, _ := hkdfExpand(shared, append([]byte(identityAuthLabel), context...), 32)

	hs, _ := NewOnlineHandshake(identityAuthKey)
	iosEphPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientRandom := make([]byte, 32)
	rand.Read(clientRandom)

	hello := signedOnlineClientHello(t, identityAuthKey, "b", "d", 1, iosEphPriv.PublicKey().Bytes(), clientRandom)
	_, _ = hs.AcceptClientHello(hello)
	hs.Destroy()

	if hs.Completed() {
		t.Fatal("should not be completed after destroy")
	}
	allZero := true
	for _, b := range hs.MacToIosKey() {
		if b != 0 {
			allZero = false
			break
		}
	}
	if !allZero {
		t.Fatal("mac-to-ios key should be zeroed after destroy")
	}
}

func TestOnlineHandshakeImplementationMatchesSharedVector(t *testing.T) {
	vectors := loadRelayCryptoVectors(t)
	macIdentity := relayVectorKeyAgreement(t, vectors.Identity.MacPrivateKey, vectors.Identity.MacPublicKey)
	identity := &RelayCryptoIdentity{privateKey: macIdentity, publicKey: macIdentity.PublicKey()}
	authKey, err := identity.DeriveIdentityAuthKey(
		decodeVectorBytes(t, vectors.Identity.IOSPublicKey),
		"brg_fixture",
		"dev_fixture",
	)
	if err != nil {
		t.Fatalf("derive implementation identity key: %v", err)
	}
	if got := base64.StdEncoding.EncodeToString(authKey); got != vectors.Identity.IdentityAuthKey {
		t.Fatalf("implementation identity key = %s, want %s", got, vectors.Identity.IdentityAuthKey)
	}

	ephemeral := relayVectorKeyAgreement(t, vectors.Online.MacEphemeralPrivateKey, vectors.Online.MacEphemeralPublicKey)
	hs := &OnlineHandshakeState{
		identityAuthKey:  authKey,
		ephemeralPrivate: ephemeral,
		ephemeralPublic:  ephemeral.PublicKey().Bytes(),
		serverRandom:     decodeVectorBytes(t, "iIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIg="),
	}
	var hello OnlineClientHello
	if err := json.Unmarshal([]byte(vectors.Online.ClientHelloCanonical), &hello); err != nil {
		t.Fatalf("decode vector client hello: %v", err)
	}
	hello.AuthTag = vectors.Online.ClientAuthTag
	response, err := hs.AcceptClientHello(hello)
	if err != nil {
		t.Fatalf("accept vector client hello: %v", err)
	}
	canonical, err := canonicalOnlineServerHello(*response)
	if err != nil {
		t.Fatalf("canonical vector server hello: %v", err)
	}
	if string(canonical) != vectors.Online.ServerHelloCanonical {
		t.Fatalf("server hello = %s, want %s", canonical, vectors.Online.ServerHelloCanonical)
	}
	if response.AuthTag != vectors.Online.ServerAuthTag {
		t.Fatalf("server auth tag = %s, want %s", response.AuthTag, vectors.Online.ServerAuthTag)
	}
	if got := base64.StdEncoding.EncodeToString(hs.IosToMacKey()); got != vectors.Online.IOSToMacKey {
		t.Fatalf("ios-to-mac key = %s, want %s", got, vectors.Online.IOSToMacKey)
	}
	if got := base64.StdEncoding.EncodeToString(hs.MacToIosKey()); got != vectors.Online.MacToIOSKey {
		t.Fatalf("mac-to-ios key = %s, want %s", got, vectors.Online.MacToIOSKey)
	}
}

// TestEnvelopeSealOpenRoundTrip 验证信封加密解密往返。
func TestEnvelopeSealOpenRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	aad := []byte(`{"version":1,"routeId":"r1"}`)
	plaintext := []byte(`{"type":"event","event":"turn_completed"}`)

	for counter := uint64(1); counter <= 5; counter++ {
		ct, err := SealEnvelope(key, counter, aad, plaintext)
		if err != nil {
			t.Fatalf("seal counter %d: %v", counter, err)
		}
		if len(ct) == 0 {
			t.Fatalf("ciphertext should not be empty at counter %d", counter)
		}

		decrypted, err := OpenEnvelope(key, counter, aad, ct)
		if err != nil {
			t.Fatalf("open counter %d: %v", counter, err)
		}
		if string(decrypted) != string(plaintext) {
			t.Fatalf("counter %d: plaintext mismatch: got %s want %s", counter, decrypted, plaintext)
		}
	}
}

// TestEnvelopeRejectsTamperedAAD 验证 AAD 篡改被拒绝。
func TestEnvelopeRejectsTamperedAAD(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	aad := []byte("original-aad")
	plaintext := []byte("secret data")

	ct, _ := SealEnvelope(key, 1, aad, plaintext)

	tamperedAAD := []byte("tampered-aad")
	_, err := OpenEnvelope(key, 1, tamperedAAD, ct)
	if err == nil {
		t.Fatal("should reject tampered AAD")
	}
}

// TestEnvelopeRejectsWrongCounter 验证错误 counter 被拒绝。
func TestEnvelopeRejectsWrongCounter(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	aad := []byte("aad")
	plaintext := []byte("data")

	ct, _ := SealEnvelope(key, 1, aad, plaintext)

	_, err := OpenEnvelope(key, 2, aad, ct)
	if err == nil {
		t.Fatal("should reject wrong counter")
	}
}

// TestEnvelopeRejectsWrongKey 验证错误密钥被拒绝。
func TestEnvelopeRejectsWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)
	aad := []byte("aad")
	plaintext := []byte("data")

	ct, _ := SealEnvelope(key1, 1, aad, plaintext)

	_, err := OpenEnvelope(key2, 1, aad, ct)
	if err == nil {
		t.Fatal("should reject wrong key")
	}
}

// TestPaddingRoundTrip 验证 padding 桶边界正确。
func TestPaddingRoundTrip(t *testing.T) {
	sizes := []int{0, 1, 100, 252, 253, 254, 255, 256, 500, 1000, 4096}
	for _, size := range sizes {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i % 256)
		}

		padded, err := applyPadding(payload)
		if err != nil {
			t.Fatalf("size %d: applyPadding error: %v", size, err)
		}
		// padded 长度应是 256 的倍数
		if len(padded)%256 != 0 {
			t.Errorf("size %d: padded length %d not multiple of 256", size, len(padded))
		}
		if len(padded) == 0 {
			t.Errorf("size %d: padded should not be empty", size)
		}

		recovered, err := removePadding(padded)
		if err != nil {
			t.Errorf("size %d: removePadding error: %v", size, err)
		}
		if len(recovered) != size {
			t.Errorf("size %d: recovered length %d != original %d", size, len(recovered), size)
		}
		for i := range recovered {
			if recovered[i] != payload[i] {
				t.Errorf("size %d: mismatch at byte %d", size, i)
				break
			}
		}
	}
}

// TestRelayEnvelopeAADSerialization 验证 AAD 序列化稳定。
func TestRelayEnvelopeAADSerialization(t *testing.T) {
	env := &RelayEnvelope{
		Version:           1,
		RouteID:           "route-1",
		SenderID:          "bridge",
		DestinationID:     "device-1",
		ChannelGeneration: 1,
		KeyEpochID:        "online",
		MessageID:         "msg-1",
		Counter:           42,
		CreatedAt:         "2026-05-24T08:00:00Z",
		ExpiresAt:         "2026-05-25T08:00:00Z",
	}

	aad1, err := env.EncodeAAD()
	if err != nil {
		t.Fatalf("encode AAD: %v", err)
	}
	aad2, err := env.EncodeAAD()
	if err != nil {
		t.Fatalf("encode AAD second: %v", err)
	}
	if string(aad1) != string(aad2) {
		t.Fatal("AAD serialization should be deterministic")
	}

	// 验证 AAD 是合法 JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(aad1, &parsed); err != nil {
		t.Fatalf("AAD should be valid JSON: %v", err)
	}
	if parsed["version"] != float64(1) {
		t.Fatal("AAD version should be 1")
	}
	if parsed["counter"] != float64(42) {
		t.Fatal("AAD counter should be 42")
	}
	for _, field := range []string{"prekeyId", "epochIndex", "epochEphemeralPublicKey", "previousEpochDigest", "epochAuthTag"} {
		if value, exists := parsed[field]; !exists || value != nil {
			t.Fatalf("online AAD field %s = %#v, want explicit null", field, value)
		}
	}

	ephemeral := "epoch-public"
	env.EpochEphemeralPublicKey = &ephemeral
	mailboxAAD, err := env.EncodeAAD()
	if err != nil {
		t.Fatalf("encode mailbox AAD: %v", err)
	}
	if err := json.Unmarshal(mailboxAAD, &parsed); err != nil {
		t.Fatalf("decode mailbox AAD: %v", err)
	}
	if parsed["epochEphemeralPublicKey"] != ephemeral {
		t.Fatalf("mailbox ephemeral key = %#v, want %q", parsed["epochEphemeralPublicKey"], ephemeral)
	}
}

// TestCounterNonceConstruction 验证 nonce 构造符合方案。
func TestCounterNonceConstruction(t *testing.T) {
	nonce := CounterNonce(1)
	// 前 4 字节应为零
	for i := 0; i < 4; i++ {
		if nonce[i] != 0 {
			t.Fatalf("nonce prefix byte %d should be zero", i)
		}
	}
	// counter = 1 应在 offset 12（大端序后 8 字节的第一个）
	if nonce[11] != 1 {
		t.Fatal("counter=1 should be at byte 11")
	}

	nonce2 := CounterNonce(256)
	if nonce2[10] != 1 || nonce2[11] != 0 {
		t.Fatal("counter=256 should be 0x100 big-endian at bytes 10-11")
	}
}

// TestRelayDeviceConnSendJSON 验证 relay conn 加密发送。
func TestRelayDeviceConnSendJSON(t *testing.T) {
	var lastEnvelope json.RawMessage
	sendFunc := func(env json.RawMessage) error {
		lastEnvelope = env
		return nil
	}

	key := make([]byte, 32)
	rand.Read(key)

	conn := NewRelayDeviceConn("dev-1", "bridge-1", "route-1", 1, nil, key, nil, sendFunc)

	msg := map[string]string{"type": "event", "event": "turn_completed"}
	conn.SendJSON(msg)

	if len(lastEnvelope) == 0 {
		t.Fatal("should have sent an envelope")
	}

	// 解析信封，验证结构
	var envelope RelayEnvelope
	if err := json.Unmarshal(lastEnvelope, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.Version != 1 {
		t.Fatal("envelope version should be 1")
	}
	if envelope.SenderID != "bridge" {
		t.Fatal("sender should be bridge")
	}
	if envelope.DestinationID != "dev-1" {
		t.Fatal("destination should be dev-1")
	}
	if envelope.Counter != 1 {
		t.Fatalf("counter should be 1, got %d", envelope.Counter)
	}
	if envelope.ContentEncoding != "" {
		t.Fatalf("legacy relay envelope content encoding = %q, want empty", envelope.ContentEncoding)
	}
	if len(envelope.Ciphertext) == 0 {
		t.Fatal("ciphertext should not be empty")
	}
	aad, err := envelope.EncodeAAD()
	if err != nil {
		t.Fatalf("encode aad: %v", err)
	}
	opened, err := OpenEnvelope(key, envelope.Counter, aad, envelope.Ciphertext)
	if err != nil {
		t.Fatalf("open encrypted relay payload: %v", err)
	}
	if string(opened) != `{"event":"turn_completed","type":"event"}` {
		t.Fatalf("opened payload = %s", opened)
	}
}

func TestRelayDeviceConnGzipNegotiationCompressesBeforeEncryption(t *testing.T) {
	var lastEnvelope json.RawMessage
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("random key: %v", err)
	}
	conn := NewRelayDeviceConn("web-1", "bridge-1", "route-1", 1, nil, key, nil, func(env json.RawMessage) error {
		lastEnvelope = env
		return nil
	})
	if !negotiateRelayGzip(conn, []string{"relay_gzip_v1"}) {
		t.Fatal("relay gzip capability should negotiate for a relay connection")
	}

	msg := map[string]string{"type": "result", "history": strings.Repeat("compressible-history-entry-", 4096)}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal expected payload: %v", err)
	}
	conn.SendJSON(msg)

	var envelope RelayEnvelope
	if err := json.Unmarshal(lastEnvelope, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.ContentEncoding != "gzip" {
		t.Fatalf("content encoding = %q, want gzip", envelope.ContentEncoding)
	}
	aad, err := envelope.EncodeAAD()
	if err != nil {
		t.Fatalf("encode aad: %v", err)
	}
	compressed, err := OpenEnvelope(key, envelope.Counter, aad, envelope.Ciphertext)
	if err != nil {
		t.Fatalf("open compressed relay payload: %v", err)
	}
	if len(compressed) >= len(raw) {
		t.Fatalf("compressed payload %d bytes is not smaller than raw %d bytes", len(compressed), len(raw))
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open gzip stream: %v", err)
	}
	opened, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip stream: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close gzip stream: %v", err)
	}
	if !bytes.Equal(opened, raw) {
		t.Fatal("decompressed payload differs from original JSON")
	}

	// contentEncoding is authenticated metadata, not an unauthenticated decompression hint.
	envelope.ContentEncoding = ""
	tamperedAAD, err := envelope.EncodeAAD()
	if err != nil {
		t.Fatalf("encode tampered aad: %v", err)
	}
	if _, err := OpenEnvelope(key, envelope.Counter, tamperedAAD, envelope.Ciphertext); err == nil {
		t.Fatal("removing authenticated contentEncoding should fail decryption")
	}
}

// TestRelayDeviceConnReceiveJSON 验证入站 envelope 解密并绑定到当前 channel。
func TestRelayDeviceConnReceiveJSON(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	conn := NewRelayDeviceConn("dev-1", "bridge-1", "route-1", 7, nil, nil, key, nil)

	newEnvelope := func(routeID string) []byte {
		envelope := RelayEnvelope{
			Version:           1,
			RouteID:           routeID,
			SenderID:          "dev-1",
			DestinationID:     "bridge",
			ChannelGeneration: 7,
			KeyEpochID:        "online:7",
			MessageID:         "msg-inbound",
			Counter:           1,
			CreatedAt:         "2026-05-25T00:00:00Z",
			ExpiresAt:         "2026-05-26T00:00:00Z",
		}
		aad, err := envelope.EncodeAAD()
		if err != nil {
			t.Fatalf("encode aad: %v", err)
		}
		envelope.Ciphertext, err = SealEnvelope(key, envelope.Counter, aad, []byte(`{"type":"request"}`))
		if err != nil {
			t.Fatalf("seal envelope: %v", err)
		}
		raw, err := json.Marshal(envelope)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		return raw
	}

	if _, err := conn.ReceiveJSON(newEnvelope("wrong-route")); err == nil {
		t.Fatal("cross-route inbound envelope must be rejected")
	}

	plaintext, err := conn.ReceiveJSON(newEnvelope("route-1"))
	if err != nil {
		t.Fatalf("receive encrypted relay payload: %v", err)
	}
	if string(plaintext) != `{"type":"request"}` {
		t.Fatalf("opened payload = %s", plaintext)
	}
}

// TestRelayDeviceConnCounterIncrements 验证 counter 严格递增。
func TestRelayDeviceConnCounterIncrements(t *testing.T) {
	var counters []uint64
	sendFunc := func(env json.RawMessage) error {
		var envelope RelayEnvelope
		json.Unmarshal(env, &envelope)
		counters = append(counters, envelope.Counter)
		return nil
	}

	key := make([]byte, 32)
	rand.Read(key)
	conn := NewRelayDeviceConn("dev-1", "bridge-1", "route-1", 1, nil, key, nil, sendFunc)

	for i := 0; i < 5; i++ {
		conn.SendJSON(map[string]string{"msg": fmt.Sprintf("msg-%d", i)})
	}

	for i, c := range counters {
		if c != uint64(i+1) {
			t.Errorf("counter[%d] = %d, want %d", i, c, i+1)
		}
	}
}

// TestRelayDeviceConnDestroyClearsKeys 验证 Close 擦除密钥。
func TestRelayDeviceConnDestroyClearsKeys(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	keyCopy := make([]byte, 32)
	copy(keyCopy, key)

	conn := NewRelayDeviceConn("dev-1", "bridge-1", "route-1", 1, nil, key, nil, func(json.RawMessage) error { return nil })
	conn.Close()

	// key 应被清零（conn 持有同一 slice）
	allZero := true
	for _, b := range key {
		if b != 0 {
			allZero = false
			break
		}
	}
	if !allZero {
		t.Fatal("key should be zeroed after close")
	}
}

// TestRelayConfigBasic 验证 relay 配置基本操作。
func TestRelayConfigBasic(t *testing.T) {
	rc := NewRelayConfig()

	if rc.Enabled() {
		t.Fatal("should start disabled")
	}
	if rc.HasRelayCapability() {
		t.Fatal("should not have capability without endpoint")
	}

	rc.SetEnabled(true)
	rc.SetEndpoint("wss://relay.example.com")
	rc.SetRouteID("route-123")
	rc.SetCredential("cred-abc")

	if !rc.Enabled() {
		t.Fatal("should be enabled")
	}
	if !rc.HasRelayCapability() {
		t.Fatal("should have capability with endpoint")
	}

	status := rc.Status()
	if !status.Enabled {
		t.Fatal("status should show enabled")
	}
	if status.Endpoint != "wss://relay.example.com" {
		t.Fatalf("endpoint = %s", status.Endpoint)
	}
}

func signedOnlineClientHello(t *testing.T, authKey []byte, bridgeID, deviceID string, generation uint64, ephemeral, random []byte) OnlineClientHello {
	t.Helper()
	hello := OnlineClientHello{
		Type:                  "online_client_hello",
		BridgeID:              bridgeID,
		DeviceID:              deviceID,
		ChannelGeneration:     generation,
		IOSEphemeralPublicKey: base64.StdEncoding.EncodeToString(ephemeral),
		ClientRandom:          base64.StdEncoding.EncodeToString(random),
	}
	canonical, err := canonicalOnlineClientHello(hello)
	if err != nil {
		t.Fatalf("canonical client hello: %v", err)
	}
	hello.AuthTag = base64.StdEncoding.EncodeToString(hmacSHA256(authKey, canonical))
	return hello
}

// TestDirectConnAdapter 验证 DirectConn 适配器行为。
func TestDirectConnAdapter(t *testing.T) {
	// 直接使用 Conn 的 mock 不现实，验证接口满足即可
	var _ Connection = adaptDirectConn(nil)
}
