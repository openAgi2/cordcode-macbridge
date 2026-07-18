package gobridge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// Nonce 前缀固定 4 字节零，后 8 字节为大端序 counter。
	// 方案 §5.5：每一方向从 counter = 1 开始，nonce 固定构造为 4 个零字节加 8 字节大端序 counter。
	noncePrefixLen  = 4
	nonceCounterLen = 8
	nonceSize       = chacha20poly1305.NonceSize // 12 for ChaCha20-Poly1305

	// Padding 桶大小
	paddingBucketSize = 256
)

// RelayEnvelope 是 Relay 外层信封。
// 方案 §7.1：Relay 只处理外层信封，内层是原始 Bridge v1 JSON message 的密文。
type RelayEnvelope struct {
	Version                 uint8               `json:"version"`
	RouteID                 string              `json:"routeId"`
	SenderID                string              `json:"senderId"`
	DestinationID           string              `json:"destinationId"`
	ChannelGeneration       uint64              `json:"channelGeneration"`
	KeyEpochID              string              `json:"keyEpochId"`
	PrekeyID                *string             `json:"prekeyId"`
	EpochIndex              *uint64             `json:"epochIndex"`
	EpochEphemeralPublicKey *string             `json:"epochEphemeralPublicKey"`
	PreviousEpochDigest     *string             `json:"previousEpochDigest"`
	EpochAuthTag            *string             `json:"epochAuthTag"`
	MessageID               string              `json:"messageId"`
	Counter                 uint64              `json:"counter"`
	ContentEncoding         string              `json:"contentEncoding,omitempty"`
	Chunk                   *RelayChunkMetadata `json:"chunk,omitempty"`
	Ciphertext              []byte              `json:"ciphertext"` // base64 in JSON, []byte in memory
	CreatedAt               string              `json:"createdAt"`
	ExpiresAt               string              `json:"expiresAt"`
}

// RelayChunkMetadata identifies one independently authenticated frame in a
// relay_chunks_v1 logical message. GroupID and Count are fixed for the group;
// Index advances in wire order.
type RelayChunkMetadata struct {
	GroupID string `json:"groupId"`
	Index   uint32 `json:"index"`
	Count   uint32 `json:"count"`
}

// AADFields 返回需要被 AEAD 校验的外层字段。
// 方案 §7.2：以下外层字段必须序列化为稳定 AAD 并被 AEAD 校验覆盖。
func (e *RelayEnvelope) AADFields() map[string]interface{} {
	aad := map[string]interface{}{
		"version":           e.Version,
		"routeId":           e.RouteID,
		"senderId":          e.SenderID,
		"destinationId":     e.DestinationID,
		"channelGeneration": e.ChannelGeneration,
		"keyEpochId":        e.KeyEpochID,
		"messageId":         e.MessageID,
		"counter":           e.Counter,
		"createdAt":         e.CreatedAt,
		"expiresAt":         e.ExpiresAt,
	}
	aad["prekeyId"] = e.PrekeyID
	aad["epochIndex"] = e.EpochIndex
	aad["epochEphemeralPublicKey"] = e.EpochEphemeralPublicKey
	aad["previousEpochDigest"] = e.PreviousEpochDigest
	aad["epochAuthTag"] = e.EpochAuthTag
	if e.ContentEncoding != "" {
		aad["contentEncoding"] = e.ContentEncoding
	}
	if e.Chunk != nil {
		// Keep nested chunk fields canonical too. encoding/json preserves struct
		// declaration order, while the other clients recursively sort object keys.
		aad["chunk"] = map[string]interface{}{
			"groupId": e.Chunk.GroupID,
			"index":   e.Chunk.Index,
			"count":   e.Chunk.Count,
		}
	}
	return aad
}

// EncodeAAD 将 AAD 字段序列化为稳定的 JSON 字节。
func (e *RelayEnvelope) EncodeAAD() ([]byte, error) {
	return json.Marshal(e.AADFields())
}

// CounterNonce 从 counter 构造 12 字节 nonce。
// 方案 §5.5：4 个零字节 + 8 字节大端序 counter = 12 字节（标准 ChaCha20-Poly1305 nonce）。
func CounterNonce(counter uint64) [nonceSize]byte {
	var nonce [nonceSize]byte
	// 前 4 字节固定为零
	// 后 8 字节为大端序 counter
	binary.BigEndian.PutUint64(nonce[noncePrefixLen:noncePrefixLen+nonceCounterLen], counter)
	return nonce
}

// SealEnvelope 加密 inner payload 并生成密文信封。
// key 是 ChaCha20-Poly1305 traffic key（32 字节）。
// counter 是发送方向计数器，从 1 开始严格递增。
//
// 方案 §5.5：inner payload 在加密前封装真实长度并填充到 256 字节桶。
func SealEnvelope(key []byte, counter uint64, aad []byte, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("create AEAD: %w", err)
	}

	// padding：封装真实长度 + 填充到 256 字节桶
	padded, err := applyPadding(plaintext)
	if err != nil {
		return nil, err
	}

	nonce := CounterNonce(counter)
	return aead.Seal(nil, nonce[:], padded, aad), nil
}

// OpenEnvelope 解密密文信封。
// 返回去掉 padding 后的原始 plaintext。
// 方案 §5.5：接收方只可提交严格下一个 counter。
func OpenEnvelope(key []byte, counter uint64, aad []byte, ciphertext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("create AEAD: %w", err)
	}

	nonce := CounterNonce(counter)
	padded, err := aead.Open(nil, nonce[:], ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	return removePadding(padded)
}

// applyPadding 将 payload 封装为：4 字节大端真实长度 + payload + padding。
// 填充到 256 字节桶边界。
func applyPadding(payload []byte) ([]byte, error) {
	realLen := uint32(len(payload))
	bucketSize := uint32(paddingBucketSize)

	// 总内容 = 4 (length prefix) + payload + padding
	totalContent := uint32(4) + realLen
	// 向上取整到桶边界
	if totalContent%bucketSize != 0 {
		totalContent = (totalContent/bucketSize + 1) * bucketSize
	}
	// 最少一个桶
	if totalContent < bucketSize {
		totalContent = bucketSize
	}
	// 不超过 uint32 范围
	if totalContent > math.MaxUint32 {
		totalContent = math.MaxUint32
	}

	result := make([]byte, totalContent)
	binary.BigEndian.PutUint32(result[:4], realLen)
	copy(result[4:], payload)
	if _, err := rand.Read(result[4+realLen:]); err != nil {
		return nil, fmt.Errorf("random relay padding: %w", err)
	}
	return result, nil
}

// removePadding 从 padded 数据中恢复原始 payload。
func removePadding(padded []byte) ([]byte, error) {
	if len(padded) < 4 {
		return nil, fmt.Errorf("padded data too short")
	}
	realLen := binary.BigEndian.Uint32(padded[:4])
	if uint32(4+realLen) > uint32(len(padded)) {
		return nil, fmt.Errorf("declared length %d exceeds padded data size %d", realLen, len(padded))
	}
	return padded[4 : 4+realLen], nil
}

// EnvelopeDigest 计算信封的认证 digest（用于 epoch chain）。
func EnvelopeDigest(envelopeBytes []byte) []byte {
	h := sha256.Sum256(envelopeBytes)
	return h[:]
}
