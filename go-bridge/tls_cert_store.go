package gobridge

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TLSPinAlgorithm 是 Bridge TLS pin 的固定算法标识。
// iOS 端 BridgeTLSPin.canonicalAlgorithm 与之一致；两端通过 ComputeSPKIPin 的
// 黄金测试 (TestComputeSPKIPin_MatchesOpenSSLCannonical) 保证口径对齐。
const TLSPinAlgorithm = "sha256-spki"

// tlsPinRotationWindow 是证书轮换时旧 pin 仍被接受的窗口长度。
// 与 docs/2026-06-19-t00-tlspin-owner-unblock-spec.md §4.1 推荐值一致（14 天），
// 覆盖 MacBridge 自动更新 + 用户手动更新 Mac 的常规周期。
const tlsPinRotationWindow = 14 * 24 * time.Hour

// storedTLSCert 是持久化到 <dataDir>/tls-cert.json 的证书 + pin 元数据。
//
// 设计要点（对照 relay_identity.go 的 LoadOrCreate 模式）：
//   - DER / PrivateKeyPKCS8 直接存原始字节（base64 编码进 JSON），加载后零拷贝重建 *tls.Certificate。
//   - Generation 单调递增，iOS 端 BridgeTLSPinMath.validate(generationAgainst:) 据此拒绝降级。
//   - PreviousSPKIBase64 / PreviousValidUntil 仅在轮换窗口内非空；窗口结束后 iOS 拒绝 previous。
type storedTLSCert struct {
	DER                  []byte     `json:"der"`                    // leaf 证书 DER
	PrivateKeyPKCS8      []byte     `json:"privateKeyPkcs8"`        // PKCS#8 私钥
	Generation           uint64     `json:"generation"`             // 单调递增
	PreviousSPKIBase64   string     `json:"previousSpki,omitempty"` // 轮换窗口内的旧 pin
	PreviousValidUntil   *time.Time `json:"previousValidUntil,omitempty"`
	NotAfter             time.Time  `json:"notAfter"` // 证书有效期，用于检测过期触发轮换
}

// tlsCertPath 返回 <dataDir>/tls-cert.json 的完整路径。
func tlsCertPath(dataDir *DataDir) string {
	if dataDir == nil {
		return ""
	}
	return filepath.Join(dataDir.Path(), "tls-cert.json")
}

// LoadOrCreateTLSCert 加载或新建一张持久化的自签名 TLS 证书，并派生其 SPKI pin。
//
// 与 generateSelfSignedCert 的关键区别：本函数把证书持久化到 dataDir，保证同一台 Mac
// 跨重启使用同一张证书（同一 SPKI），从而 iOS 端可以固定 pin。
//
// dataDir 为 nil 时（开发/测试）退化为一次性生成，不持久化——保持 resolveTailscaleRemote
// 在无 dataDir 场景的原有行为。
func LoadOrCreateTLSCert(dataDir *DataDir, hosts ...string) (*tls.Certificate, *BridgeV1TLSPin, error) {
	path := tlsCertPath(dataDir)

	// dataDir 为 nil（开发/测试）：完全退化为一次性 generateSelfSignedCert，
	// 不持久化、不派生 pin。保持 resolveTailscaleRemote 在无 dataDir 场景的原有行为。
	if path == "" {
		cert, err := generateSelfSignedCert(hosts...)
		if err != nil {
			return nil, nil, fmt.Errorf("generate TLS cert: %w", err)
		}
		return cert, nil, nil
	}

	// 尝试加载已持久化的证书。
	if stored, err := loadStoredTLSCert(path); err == nil && stored != nil {
		cert, err := stored.toTLSCertificate()
		if err == nil {
			pin, err := pinFromStored(stored)
			if err == nil {
				slog.Info("go-bridge: loaded persisted TLS cert",
					"generation", stored.Generation,
					"hasPrevious", stored.PreviousSPKIBase64 != "")
				return cert, pin, nil
			}
			slog.Warn("go-bridge: persisted TLS cert pin derive failed, regenerating", "error", err)
		} else {
			slog.Warn("go-bridge: persisted TLS cert rebuild failed, regenerating", "error", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		slog.Warn("go-bridge: persisted TLS cert read failed, regenerating", "error", err)
	}

	// 不存在或损坏 → 生成新证书。
	cert, err := generateSelfSignedCert(hosts...)
	if err != nil {
		return nil, nil, fmt.Errorf("generate TLS cert: %w", err)
	}

	spi, err := ComputeSPKIPin(cert.Certificate[0])
	if err != nil {
		return nil, nil, fmt.Errorf("compute SPKI pin: %w", err)
	}

	stored, err := newStoredFromCert(cert, 1, "", nil)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal new TLS cert: %w", err)
	}

	pin := &BridgeV1TLSPin{
		Algorithm:  TLSPinAlgorithm,
		Value:      spi,
		Generation: 1,
	}

	if err := saveStoredTLSCert(path, stored); err != nil {
		slog.Error("go-bridge: persist TLS cert failed (using in-memory)", "error", err)
		// 不致命：返回内存态证书 + pin，启动可继续；下次重启会重新生成。
	}
	return cert, pin, nil
}

// RotateTLSCert 生成新证书并把旧 pin 塞进轮换窗口。
// generation 递增；旧 SPKI 在 tlsPinRotationWindow 内仍被 iOS 接受。
// 首版不自动触发（证书有效期 1 年），提供 API 供后续手动/定时调用。
func RotateTLSCert(dataDir *DataDir, hosts ...string) (*tls.Certificate, *BridgeV1TLSPin, error) {
	path := tlsCertPath(dataDir)
	var prevStored *storedTLSCert
	if path != "" {
		if s, err := loadStoredTLSCert(path); err == nil {
			prevStored = s
		}
	}

	cert, err := generateSelfSignedCert(hosts...)
	if err != nil {
		return nil, nil, fmt.Errorf("generate rotated TLS cert: %w", err)
	}
	newSPI, err := ComputeSPKIPin(cert.Certificate[0])
	if err != nil {
		return nil, nil, fmt.Errorf("compute rotated SPKI pin: %w", err)
	}

	nextGen := uint64(1)
	var prevSPKI string
	var prevValidUntil *time.Time
	if prevStored != nil {
		nextGen = prevStored.Generation + 1
		// 旧 pin 作为 previous；其 SPKI 必须用旧证书重新计算（不信任 stored 里的缓存值，
		// 防止存储被篡改后把攻击者 pin 写进 previous）。
		if oldCert, rebuildErr := prevStored.toTLSCertificate(); rebuildErr == nil {
			if oldSPI, computeErr := ComputeSPKIPin(oldCert.Certificate[0]); computeErr == nil {
				prevSPKI = oldSPI
				t := time.Now().Add(tlsPinRotationWindow)
				prevValidUntil = &t
			}
		}
	}

	stored, err := newStoredFromCert(cert, nextGen, prevSPKI, prevValidUntil)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal rotated TLS cert: %w", err)
	}

	pin := &BridgeV1TLSPin{
		Algorithm:       TLSPinAlgorithm,
		Value:           newSPI,
		Generation:      nextGen,
		PreviousValue:   prevSPKI,
	}
	if prevValidUntil != nil {
		pin.PreviousValidUntilMillis = prevValidUntil.UnixMilli()
	}

	if path != "" {
		if err := saveStoredTLSCert(path, stored); err != nil {
			return nil, nil, fmt.Errorf("persist rotated TLS cert: %w", err)
		}
	}
	slog.Info("go-bridge: TLS cert rotated",
		"generation", nextGen,
		"hasPrevious", prevSPKI != "")
	return cert, pin, nil
}

// ComputeSPKIPin 计算一张 DER 证书的 sha256-spki pin。
//
// 口径（必须与 iOS 端 BridgeTLSPinMath + OpenSSL 对齐）：
//   base64(SHA256(SubjectPublicKeyInfo DER))
//
// 等价 OpenSSL：
//   openssl x509 -pubkey -noout | openssl pkey -pubin -outform DER \
//     | openssl dgst -sha256 -binary | base64
//
// Go 端用 x509.MarshalPKIXPublicKey(pubkey) 产出与 OpenSSL `-outform DER` 相同的 SPKI DER。
func ComputeSPKIPin(derCert []byte) (string, error) {
	cert, err := x509.ParseCertificate(derCert)
	if err != nil {
		return "", fmt.Errorf("parse certificate: %w", err)
	}
	spkiDER, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return "", fmt.Errorf("marshal SPKI: %w", err)
	}
	sum := sha256.Sum256(spkiDER)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// ── storedTLSCert I/O ────────────────────────────────────────────────────────

func loadStoredTLSCert(path string) (*storedTLSCert, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s storedTLSCert
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal tls-cert.json: %w", err)
	}
	if len(s.DER) == 0 || len(s.PrivateKeyPKCS8) == 0 {
		return nil, fmt.Errorf("tls-cert.json missing DER or privateKey")
	}
	return &s, nil
}

func saveStoredTLSCert(path string, s *storedTLSCert) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tls-cert.json: %w", err)
	}
	data = append(data, '\n')
	// 原子写 + 0600（与 identity.json / relay_identity.key 同权限）。
	return core.AtomicWriteFile(path, data, 0o600)
}

func (s *storedTLSCert) toTLSCertificate() (*tls.Certificate, error) {
	priv, err := x509.ParsePKCS8PrivateKey(s.PrivateKeyPKCS8)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS8 private key: %w", err)
	}
	rsaPriv, ok := priv.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("stored private key is not RSA")
	}
	return &tls.Certificate{
		Certificate: [][]byte{s.DER},
		PrivateKey:  rsaPriv,
	}, nil
}

func pinFromStored(s *storedTLSCert) (*BridgeV1TLSPin, error) {
	spi, err := ComputeSPKIPin(s.DER)
	if err != nil {
		return nil, err
	}
	pin := &BridgeV1TLSPin{
		Algorithm:  TLSPinAlgorithm,
		Value:      spi,
		Generation: s.Generation,
	}
	if s.PreviousSPKIBase64 != "" {
		pin.PreviousValue = s.PreviousSPKIBase64
	}
	if s.PreviousValidUntil != nil {
		pin.PreviousValidUntilMillis = s.PreviousValidUntil.UnixMilli()
	}
	return pin, nil
}

func newStoredFromCert(cert *tls.Certificate, generation uint64, prevSPKI string, prevValidUntil *time.Time) (*storedTLSCert, error) {
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("cert has no DER bytes")
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal private key PKCS8: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse leaf for NotAfter: %w", err)
	}
	return &storedTLSCert{
		DER:                cert.Certificate[0],
		PrivateKeyPKCS8:    pkcs8,
		Generation:         generation,
		PreviousSPKIBase64: prevSPKI,
		PreviousValidUntil: prevValidUntil,
		NotAfter:           leaf.NotAfter,
	}, nil
}
