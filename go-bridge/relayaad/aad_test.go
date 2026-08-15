package relayaad

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestCanonicalChunkAAD_Determinism(t *testing.T) {
	a := CanonicalChunkAAD("grp-1", 0, 3, "")
	b := CanonicalChunkAAD("grp-1", 0, 3, "")
	if !bytes.Equal(a, b) {
		t.Error("same input must yield identical AAD")
	}
}

func TestCanonicalChunkAAD_BaseVsCorrelatedDiffer(t *testing.T) {
	base := CanonicalChunkAAD("grp-1", 0, 3, "")
	corr := CanonicalChunkAAD("grp-1", 0, 3, "0123456789abcdef0123456789abcdef")
	if bytes.Equal(base, corr) {
		t.Error("base and correlated AAD must differ (correlation field presence is bound)")
	}
}

func TestCanonicalChunkAAD_TamperEachFieldChangesAAD(t *testing.T) {
	base := CanonicalChunkAAD("grp-1", 2, 5, "0123456789abcdef0123456789abcdef")
	cases := map[string][]byte{
		"groupId changed":     CanonicalChunkAAD("grp-2", 2, 5, "0123456789abcdef0123456789abcdef"),
		"index changed":       CanonicalChunkAAD("grp-1", 3, 5, "0123456789abcdef0123456789abcdef"),
		"count changed":       CanonicalChunkAAD("grp-1", 2, 6, "0123456789abcdef0123456789abcdef"),
		"correlation changed": CanonicalChunkAAD("grp-1", 2, 5, "fedcba9876543210fedcba9876543210"),
		"correlation dropped": CanonicalChunkAAD("grp-1", 2, 5, ""),
	}
	for name, aad := range cases {
		if bytes.Equal(base, aad) {
			t.Errorf("%s: AAD must differ from base", name)
		}
	}
}

// committed vector：固定输入 -> 固定 AAD sha256，锁定 framing（任何构造改动破坏此断言）。
func TestCanonicalChunkAAD_CommittedVector(t *testing.T) {
	aad := CanonicalChunkAAD("grp-0001-test", 0, 13, "0123456789abcdef0123456789abcdef")
	got := sha256.Sum256(aad)
	// 真实输出捕获（首跑得到），固化于此：domain/field-order/encoding 任一变化都会破坏。
	const wantHex = "4b97579cdd9f3396134184cce4f86cf96e3db8b2897c14e96b1d8831d44a5b38"
	if hex.EncodeToString(got[:]) != wantHex {
		t.Errorf("committed AAD sha256 drifted:\n got  %s\n want %s (len=%d)", hex.EncodeToString(got[:]), wantHex, len(aad))
	}
	if !bytes.HasPrefix(aad, []byte(domain)) {
		t.Error("AAD must start with canonical domain")
	}
}

// AEAD binding：用固定测试密钥 + AES-GCM 证明 AAD 被密码学绑进密文。
// （真实 Relay 用 HPKE 派生密钥；AAD 的绑定语义对任何 AEAD 相同。）
func TestAEAD_AADBinding(t *testing.T) {
	// 固定测试密钥（32 字节 AES-256）+ nonce（12 字节）。非生产密钥；仅证明 AAD 绑定。
	key := bytes.Repeat([]byte{0xAB}, 32)
	nonce := bytes.Repeat([]byte{0xCD}, 12)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"method":"read_file_v2","params":{}}`)
	aad := CanonicalChunkAAD("grp-1", 0, 3, "0123456789abcdef0123456789abcdef")

	ct := gcm.Seal(nil, nonce, plaintext, aad)

	// 1) 正确 AAD 解密还原原文
	got, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		t.Fatalf("decrypt with correct AAD failed: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Error("decrypted plaintext mismatch")
	}

	// 2) 篡改 AAD（任一字段）→ 解密失败（认证失败）
	tamperedAAD := CanonicalChunkAAD("grp-2", 0, 3, "0123456789abcdef0123456789abcdef") // groupId 改
	if _, err := gcm.Open(nil, nonce, ct, tamperedAAD); err == nil {
		t.Error("decrypt with tampered AAD must FAIL (AAD is authenticated)")
	}

	// 3) base vs correlated AAD 互换 → 解密失败（防 base/correlated 偷换）
	baseAAD := CanonicalChunkAAD("grp-1", 0, 3, "")
	if _, err := gcm.Open(nil, nonce, ct, baseAAD); err == nil {
		t.Error("decrypt with base AAD over correlated ciphertext must FAIL")
	}

	// 4) 篡改密文 → 解密失败
	ctTampered := append([]byte{}, ct...)
	ctTampered[len(ctTampered)-1] ^= 0xFF
	if _, err := gcm.Open(nil, nonce, ctTampered, aad); err == nil {
		t.Error("decrypt with tampered ciphertext must FAIL")
	}
}
