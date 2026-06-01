package gobridge

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// ─── Phase 3 HPKE Pairing Regression Gate ────────────────────────────────

// TestRegressionR1_QRContainsBridgeFingerprint 验证 QR 包含正确 fingerprint。
func TestRegressionR1_QRContainsBridgeFingerprint(t *testing.T) {
	priv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pub := priv.PublicKey().Bytes()

	qr, _ := GeneratePairingQR("route_fp_test", pub, "wss://relay.example.com")

	h := sha256.Sum256(pub)
	expectedFP := base64.RawURLEncoding.EncodeToString(h[:])
	if qr.BridgeFP != expectedFP {
		t.Errorf("fingerprint mismatch:\n  QR:  %s\n  want %s", qr.BridgeFP, expectedFP)
	}

	// QR 不应包含私钥
	qrJSON, _ := json.Marshal(qr)
	s := string(qrJSON)
	privB64 := base64.StdEncoding.EncodeToString(priv.Bytes())
	if containsStr(s, privB64) {
		t.Error("QR should not contain private key material")
	}
}

// TestRegressionR2_ClaimApproveInterop 验证完整 claim→approve 互操作。
func TestRegressionR2_ClaimApproveInterop(t *testing.T) {
	bridgePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bridgePub := bridgePriv.PublicKey().Bytes()
	devicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	devicePub := devicePriv.PublicKey().Bytes()

	qr, _ := GeneratePairingQR("route_interop", bridgePub, "wss://relay.example.com")
	claim, _ := CreatePairingClaim("dev_interop", "iPhone", devicePub, bridgePub)

	approve, err := ProcessPairingClaim(claim, bridgePriv.Bytes())
	if err != nil {
		t.Fatalf("ProcessPairingClaim: %v", err)
	}
	if !approve.Approved {
		t.Errorf("should be approved, reason: %s", approve.Reason)
	}
	if approve.DeviceID != "dev_interop" {
		t.Errorf("deviceID = %q", approve.DeviceID)
	}
	_ = qr.RouteID
}

// TestRegressionR3_PublicKeySubstitutionRejected 验证 public key 替换被拒绝。
func TestRegressionR3_PublicKeySubstitutionRejected(t *testing.T) {
	realPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	realPub := realPriv.PublicKey().Bytes()
	attackerPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	attackerPub := attackerPriv.PublicKey().Bytes()
	devicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	devicePub := devicePriv.PublicKey().Bytes()

	// iOS 用真实 key 加密，攻击者尝试解密
	claim, _ := CreatePairingClaim("dev_atk1", "iPhone", devicePub, realPub)
	_, err := ProcessPairingClaim(claim, attackerPriv.Bytes())
	if err == nil {
		t.Error("attacker should not decrypt claim for real bridge key")
	}

	// 攻击者替换 QR 公钥，真实 bridge 无法解密
	claim2, _ := CreatePairingClaim("dev_atk2", "iPhone", devicePub, attackerPub)
	_, err = ProcessPairingClaim(claim2, realPriv.Bytes())
	if err == nil {
		t.Error("real bridge should not decrypt claim for attacker key")
	}
}

// TestRegressionR4_ClaimReplayWithinTTL 验证 claim 在 TTL 内可重放。
func TestRegressionR4_ClaimReplayWithinTTL(t *testing.T) {
	bridgePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bridgePub := bridgePriv.PublicKey().Bytes()
	devicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	devicePub := devicePriv.PublicKey().Bytes()

	claim, _ := CreatePairingClaim("dev_replay", "iPhone", devicePub, bridgePub)

	approve1, _ := ProcessPairingClaim(claim, bridgePriv.Bytes())
	if !approve1.Approved {
		t.Error("first processing should succeed")
	}
	approve2, _ := ProcessPairingClaim(claim, bridgePriv.Bytes())
	if !approve2.Approved {
		t.Error("replay within TTL should still succeed")
	}
}

// TestRegressionR5_ExpiredClaimRejected 验证过期 claim 被拒绝。
func TestRegressionR5_ExpiredClaimRejected(t *testing.T) {
	bridgePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bridgePub := bridgePriv.PublicKey().Bytes()
	devicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	devicePub := devicePriv.PublicKey().Bytes()

	claim, _ := CreatePairingClaim("dev_exp", "iPhone", devicePub, bridgePub)
	claim.Timestamp = time.Now().Add(-1 * time.Hour).Unix()

	approve, _ := ProcessPairingClaim(claim, bridgePriv.Bytes())
	if approve.Approved {
		t.Error("expired claim should be rejected")
	}
}

// TestRegressionR6_MultipleDevicesCanPair 验证多设备可以分别配对。
func TestRegressionR6_MultipleDevicesCanPair(t *testing.T) {
	bridgePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bridgePub := bridgePriv.PublicKey().Bytes()

	for i := 0; i < 3; i++ {
		devPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
		devPub := devPriv.PublicKey().Bytes()

		claim, _ := CreatePairingClaim(
			"dev_multi_"+string(rune('A'+i)),
			"iPhone "+string(rune('0'+i)),
			devPub,
			bridgePub,
		)

		approve, err := ProcessPairingClaim(claim, bridgePriv.Bytes())
		if err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
		if !approve.Approved {
			t.Errorf("device %d should be approved", i)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || jsonContains(s, sub))
}

func jsonContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
