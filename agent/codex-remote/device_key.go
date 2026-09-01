package codexremote

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

const (
	deviceKeyAlgorithm       = "ecdsa_p256_sha256"
	deviceKeyProtectionClass = "os_protected_nonextractable"
	signPayloadDomain        = "codex-device-key-sign-payload/v1"
)

type deviceKey struct {
	KeyID                  string
	PublicKeySpkiDerBase64 string
	Algorithm              string
	ProtectionClass        string
	private                *ecdsa.PrivateKey
}

type deviceKeyStore struct {
	mu   sync.Mutex
	keys map[string]*deviceKey
}

func randomKeyID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newDeviceKeyStore() *deviceKeyStore {
	return &deviceKeyStore{keys: map[string]*deviceKey{}}
}

func (s *deviceKeyStore) Create() (*deviceKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	spki, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	key := &deviceKey{
		KeyID:                  "org.openagi.cordcode.codex-remote." + randomKeyID(),
		PublicKeySpkiDerBase64: base64.StdEncoding.EncodeToString(spki),
		Algorithm:              deviceKeyAlgorithm,
		ProtectionClass:        deviceKeyProtectionClass,
		private:                priv,
	}
	s.mu.Lock()
	s.keys[key.KeyID] = key
	s.mu.Unlock()
	return key, nil
}

func (s *deviceKeyStore) Put(key *deviceKey) {
	if key == nil || key.KeyID == "" {
		return
	}
	s.mu.Lock()
	s.keys[key.KeyID] = key
	s.mu.Unlock()
}

func (s *deviceKeyStore) Sign(keyID string, payload []byte) (string, error) {
	s.mu.Lock()
	key := s.keys[keyID]
	s.mu.Unlock()
	if key == nil || key.private == nil {
		return "", fmt.Errorf("codex-remote: device key not found")
	}
	sum := sha256.Sum256(payload)
	der, err := ecdsa.SignASN1(rand.Reader, key.private, sum[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

func (k *deviceKey) identityJSON() []byte {
	raw, _ := json.Marshal(struct {
		Algorithm              string `json:"algorithm"`
		KeyID                  string `json:"keyId"`
		ProtectionClass        string `json:"protectionClass"`
		PublicKeySpkiDerBase64 string `json:"publicKeySpkiDerBase64"`
	}{k.Algorithm, k.KeyID, k.ProtectionClass, k.PublicKeySpkiDerBase64})
	return raw
}

func (k *deviceKey) identityHash() string {
	sum := sha256.Sum256(k.identityJSON())
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *deviceKeyStore) signEnrollment(key *deviceKey, payload any) (signedPayloadB64, signatureB64 string, err error) {
	inner, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	envelope, err := json.Marshal(struct {
		Domain  string          `json:"domain"`
		Payload json.RawMessage `json:"payload"`
	}{Domain: signPayloadDomain, Payload: inner})
	if err != nil {
		return "", "", err
	}
	sig, err := s.Sign(key.KeyID, envelope)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(envelope), sig, nil
}
