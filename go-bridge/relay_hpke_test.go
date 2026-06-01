package gobridge

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// ─── HPKE 配对测试 ──────────────────────────────────────────────────────

// TestHPKERFC9180Vector 验证 HPKE Base Mode 与 RFC 9180 test vector 一致。
func TestHPKERFC9180Vector(t *testing.T) {
	hpke, err := loadHPKEVector()
	if err != nil {
		t.Skipf("skip: HPKE vector not found: %v", err)
	}
	if hpke.Source != "RFC9180-test-vectors" {
		t.Skip("no HPKE test vector")
	}

	// 解码 test vector
	skR := mustDecodeHex(hpke.RecipientPrivateKeyHex)
	pkE := mustDecodeHex(hpke.EncapsulatedKeyHex)
	info := mustDecodeHex(hpke.InfoHex)
	aad := mustDecodeHex(hpke.AADHex)
	plaintext := mustDecodeHex(hpke.PlaintextHex)
	expectedCT := mustDecodeHex(hpke.CiphertextHex)

	// Open (recipient side)
	ct := &HPKECiphertext{
		KEMOutput:  pkE,
		Ciphertext: expectedCT,
	}

	decrypted, _, err := HPKEOpen(skR, info, aad, ct)
	if err != nil {
		t.Fatalf("HPKEOpen: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted mismatch:\n  got:    %x\n  expect: %x", decrypted, plaintext)
	}

	// circl 管理内部密钥材料，不再暴露 exporterSecret。
	// 核心验证：RFC 9180 密文解密成功即证明 Key Schedule 正确。
}

// TestHPKESealOpenRoundtrip 验证完整的 seal/open 往返。
func TestHPKESealOpenRoundtrip(t *testing.T) {
	// 生成 recipient key pair
	recipientPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkR := recipientPriv.PublicKey().Bytes()
	skR := recipientPriv.Bytes()

	info := []byte("cccode-relay/pairing/v1")
	aad := []byte("pairing:test-device:2026-05-24")
	plaintext := []byte(`{"deviceId":"dev_test","devicePubKey":"base64key","timestamp":12345}`)

	// Seal
	ct, sealCtx, err := HPKESeal(pkR, info, aad, plaintext)
	if err != nil {
		t.Fatalf("HPKESeal: %v", err)
	}
	if len(ct.KEMOutput) != 32 {
		t.Errorf("KEM output length = %d, want 32", len(ct.KEMOutput))
	}
	if sealCtx == nil {
		t.Error("seal context should exist")
	}

	// Open
	decrypted, openCtx, err := HPKEOpen(skR, info, aad, ct)
	if err != nil {
		t.Fatalf("HPKEOpen: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted mismatch")
	}
	if openCtx == nil {
		t.Error("open context should exist")
	}
}

// TestHPKEKeySubstitutionRejected 验证错误密钥无法解密。
func TestHPKEKeySubstitutionRejected(t *testing.T) {
	recipientPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	wrongPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	pkR := recipientPriv.PublicKey().Bytes()
	skRWrong := wrongPriv.Bytes()

	ct, _, _ := HPKESeal(pkR, nil, nil, []byte("secret"))

	_, _, err := HPKEOpen(skRWrong, nil, nil, ct)
	if err == nil {
		t.Error("should reject with wrong private key")
	}
}

// TestHPKETamperRejected 验证密文篡改被拒绝。
func TestHPKETamperRejected(t *testing.T) {
	recipientPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pkR := recipientPriv.PublicKey().Bytes()
	skR := recipientPriv.Bytes()

	ct, _, _ := HPKESeal(pkR, nil, nil, []byte("secret"))

	// 篡改密文
	tampered := &HPKECiphertext{
		KEMOutput:  ct.KEMOutput,
		Ciphertext: make([]byte, len(ct.Ciphertext)),
	}
	copy(tampered.Ciphertext, ct.Ciphertext)
	tampered.Ciphertext[0] ^= 0xff

	_, _, err := HPKEOpen(skR, nil, nil, tampered)
	if err == nil {
		t.Error("should reject tampered ciphertext")
	}
}

// TestHPKEAADReplayRejected 验证 AAD 不匹配时拒绝。
func TestHPKEAADReplayRejected(t *testing.T) {
	recipientPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pkR := recipientPriv.PublicKey().Bytes()
	skR := recipientPriv.Bytes()

	ct, _, _ := HPKESeal(pkR, nil, []byte("original-aad"), []byte("secret"))

	_, _, err := HPKEOpen(skR, nil, []byte("wrong-aad"), ct)
	if err == nil {
		t.Error("should reject with wrong AAD")
	}
}

// TestPairingQRRoundtrip 验证配对 QR 生成和解析。
func TestPairingQRRoundtrip(t *testing.T) {
	bridgePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bridgePub := bridgePriv.PublicKey().Bytes()

	qr, err := GeneratePairingQR("route_test", bridgePub, "wss://relay.example.com")
	if err != nil {
		t.Fatal(err)
	}

	if qr.Version != 1 {
		t.Errorf("version = %d, want 1", qr.Version)
	}
	if qr.RouteID != "route_test" {
		t.Errorf("routeID = %q", qr.RouteID)
	}
	if qr.BridgePubKey != base64.StdEncoding.EncodeToString(bridgePub) {
		t.Error("bridge pub key mismatch")
	}
	if qr.BridgeFP == "" {
		t.Error("fingerprint should not be empty")
	}
	if qr.RelayEndpoint != "wss://relay.example.com" {
		t.Errorf("endpoint = %q", qr.RelayEndpoint)
	}

	// QR 应可序列化
	qrJSON, err := json.Marshal(qr)
	if err != nil {
		t.Fatalf("marshal QR: %v", err)
	}

	var parsed PairingQR
	if err := json.Unmarshal(qrJSON, &parsed); err != nil {
		t.Fatalf("unmarshal QR: %v", err)
	}
	if parsed.RouteID != qr.RouteID {
		t.Error("roundtrip routeID mismatch")
	}
}

// TestPairingClaimApproveRoundtrip 验证完整配对流程。
func TestPairingClaimApproveRoundtrip(t *testing.T) {
	// Mac side: generate identity
	bridgePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bridgePub := bridgePriv.PublicKey().Bytes()

	// iOS side: generate device key
	devicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	devicePub := devicePriv.PublicKey().Bytes()

	// iOS: create pairing claim
	claim, err := CreatePairingClaim("dev_pair_test", "iPhone 16 Pro", devicePub, bridgePub)
	if err != nil {
		t.Fatalf("CreatePairingClaim: %v", err)
	}

	if claim.DeviceID != "dev_pair_test" {
		t.Errorf("deviceID = %q", claim.DeviceID)
	}
	if claim.HPKECiphertext == nil {
		t.Error("HPKE ciphertext should exist")
	}

	// Mac: process claim
	approve, err := ProcessPairingClaim(claim, bridgePriv.Bytes())
	if err != nil {
		t.Fatalf("ProcessPairingClaim: %v", err)
	}

	if !approve.Approved {
		t.Errorf("claim should be approved, reason: %s", approve.Reason)
	}
	if approve.DeviceID != "dev_pair_test" {
		t.Errorf("approve deviceID = %q", approve.DeviceID)
	}
	if approve.DeviceAuth == "" {
		t.Error("device auth should be generated")
	}
}

// TestPairingClaimExpiredRejected 验证过期 claim 被拒绝。
func TestPairingClaimExpiredRejected(t *testing.T) {
	bridgePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bridgePub := bridgePriv.PublicKey().Bytes()
	devicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	devicePub := devicePriv.PublicKey().Bytes()

	claim, _ := CreatePairingClaim("dev_expired", "iPhone", devicePub, bridgePub)

	// 篡改时间戳为过去
	claim.Timestamp = 0 // Unix epoch, 远超 TTL

	approve, err := ProcessPairingClaim(claim, bridgePriv.Bytes())
	if err != nil {
		t.Fatalf("ProcessPairingClaim: %v", err)
	}
	if approve.Approved {
		t.Error("expired claim should be rejected")
	}
	if approve.Reason != "claim expired" {
		t.Errorf("reason = %q, want 'claim expired'", approve.Reason)
	}
}

// TestPairingClaimWrongBridgeKeyRejected 验证错误 bridge key 无法解密。
func TestPairingClaimWrongBridgeKeyRejected(t *testing.T) {
	bridgePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bridgePub := bridgePriv.PublicKey().Bytes()
	wrongPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	devicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	devicePub := devicePriv.PublicKey().Bytes()

	// iOS 用正确的 bridge pub key 加密
	claim, _ := CreatePairingClaim("dev_wrong_key", "iPhone", devicePub, bridgePub)

	// Mac 用错误的 private key 解密
	_, err := ProcessPairingClaim(claim, wrongPriv.Bytes())
	if err == nil {
		t.Error("should reject with wrong bridge private key")
	}
}

// TestHPKEExportSecret 验证 exporter secret 派生。
func TestHPKEExportSecret(t *testing.T) {
	recipientPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pkR := recipientPriv.PublicKey().Bytes()

	ct, senderCtx, err := HPKESeal(pkR, nil, nil, []byte("test"))
	if err != nil {
		t.Fatalf("HPKESeal: %v", err)
	}
	_, receiverCtx, err := HPKEOpen(recipientPriv.Bytes(), nil, nil, ct)
	if err != nil {
		t.Fatalf("HPKEOpen: %v", err)
	}

	exported, err := HPKEExportSecret(senderCtx, []byte("pairing-shared-key"), 32)
	if err != nil {
		t.Fatalf("HPKEExportSecret: %v", err)
	}
	if len(exported) != 32 {
		t.Errorf("exported length = %d, want 32", len(exported))
	}

	// 不同 context 产生不同 secret
	received, err := HPKEExportSecret(receiverCtx, []byte("pairing-shared-key"), 32)
	if err != nil {
		t.Fatalf("receiver HPKEExportSecret: %v", err)
	}
	if string(exported) != string(received) {
		t.Error("sender and receiver exporter values must match")
	}

	exported2, _ := HPKEExportSecret(senderCtx, []byte("other-context"), 32)
	if string(exported) == string(exported2) {
		t.Error("different exporter context should produce different secrets")
	}

	if _, err := HPKEExportSecret(&HPKEContext{}, []byte("invalid-context"), 32); err == nil {
		t.Error("empty HPKE context must fail closed")
	}
}

// TestPairingClaimDeviceIDMismatchRejected 验证篡改外部 device ID 导致解密失败。
// AAD 包含 device ID，篡改外部 ID 会使 AAD 不匹配，HPKE 解密直接失败。
func TestPairingClaimDeviceIDMismatchRejected(t *testing.T) {
	bridgePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bridgePub := bridgePriv.PublicKey().Bytes()

	devicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	devicePub := devicePriv.PublicKey().Bytes()

	claim, _ := CreatePairingClaim("dev_correct", "iPhone", devicePub, bridgePub)
	claim.DeviceID = "dev_tampered"

	_, err := ProcessPairingClaim(claim, bridgePriv.Bytes())
	if err == nil {
		t.Error("tampered device ID should cause HPKE decryption failure (AAD mismatch)")
	}
}

// TestCryptoVectorHPKEField 验证 crypto vector JSON 中 hpkeBaseMode 字段可解析。
func TestCryptoVectorHPKEField(t *testing.T) {
	data, err := loadCryptoVectorsRaw()
	if err != nil {
		t.Skipf("vectors not found: %v", err)
	}

	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)

	hpkeRaw, ok := raw["hpkeBaseMode"]
	if !ok {
		t.Skip("no hpkeBaseMode in crypto vectors")
	}

	var hpke struct {
		Source                string `json:"source"`
		KEMID                 int    `json:"kemId"`
		KDFID                 int    `json:"kdfId"`
		AEADID                int    `json:"aeadId"`
		RecipientPublicKeyHex string `json:"recipientPublicKeyHex"`
	}
	if err := json.Unmarshal(hpkeRaw, &hpke); err != nil {
		t.Fatalf("parse hpke vector: %v", err)
	}

	if hpke.KEMID != 32 {
		t.Errorf("kemId = %d, want 32 (X25519)", hpke.KEMID)
	}
	if hpke.KDFID != 1 {
		t.Errorf("kdfId = %d, want 1 (SHA256)", hpke.KDFID)
	}
	if hpke.AEADID != 3 {
		t.Errorf("aeadId = %d, want 3 (ChaCha20Poly1305)", hpke.AEADID)
	}

	// 验证 recipient public key 可解析为 X25519 public key
	pkBytes := mustDecodeHex(hpke.RecipientPublicKeyHex)
	if _, err := ecdh.X25519().NewPublicKey(pkBytes); err != nil {
		t.Errorf("parse recipient public key: %v", err)
	}

}

func loadCryptoVectorsRaw() ([]byte, error) {
	return os.ReadFile("testdata/relay-v1/crypto_vectors.json")
}

type hpkeTestVector struct {
	Source                 string `json:"source"`
	KEMID                  int    `json:"kemId"`
	KDFID                  int    `json:"kdfId"`
	AEADID                 int    `json:"aeadId"`
	InfoHex                string `json:"infoHex"`
	RecipientPrivateKeyHex string `json:"recipientPrivateKeyHex"`
	RecipientPublicKeyHex  string `json:"recipientPublicKeyHex"`
	EncapsulatedKeyHex     string `json:"encapsulatedKeyHex"`
	AADHex                 string `json:"aadHex"`
	PlaintextHex           string `json:"plaintextHex"`
	CiphertextHex          string `json:"ciphertextHex"`
}

func loadHPKEVector() (*hpkeTestVector, error) {
	data, err := os.ReadFile("testdata/relay-v1/crypto_vectors.json")
	if err != nil {
		return nil, err
	}
	var raw struct {
		HPKEBaseMode json.RawMessage `json:"hpkeBaseMode"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if len(raw.HPKEBaseMode) == 0 {
		return nil, fmt.Errorf("no hpkeBaseMode field")
	}
	var v hpkeTestVector
	if err := json.Unmarshal(raw.HPKEBaseMode, &v); err != nil {
		return nil, err
	}
	return &v, nil
}
