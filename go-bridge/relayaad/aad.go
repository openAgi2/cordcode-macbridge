// Package relayaad 锁定 Relay chunk envelope 的 canonical AAD 字节构造（plan §3.6.4 / A-1.3）。
//
// AAD 是 AEAD 的「附加认证数据」：不加密，但与密文绑定——任一 AAD 字节变化都使解密失败。
// 这把 chunk metadata（groupId/index/count[/bulkCorrelationId]）cryptographically 绑进密文，
// 防止 chunk 被篡改、重排、或在 base/correlated 之间偷换。
//
// 实际 Relay 用 HPKE 派生密钥（macbridge relay-server / RuntimeBridgeClient）；本包只冻结
// canonical AAD framing，并用标准 AEAD（AES-GCM）+ 固定测试密钥证明 AAD 的绑定语义。
// versioned canonical 字段顺序变化必须 bump version；decoder 先做 strict raw-object 校验，
// 再构造 AAD（extra/duplicate/case 在 strict decode 阶段被拒，不进入 AAD）。
package relayaad

import (
	"bytes"
	"encoding/binary"
)

// domain + version：canonical framing 的稳定性边界。字段顺序/编码变化必须 bump Version。
const domain = "cordcode.relay-chunk-aad.v1\x00"
const Version uint32 = 1

// CanonicalChunkAAD 构造 chunk envelope 的 canonical AAD 字节。
//   - correlationID == "" => base AAD（{groupId,index,count}，无 correlation 字段）；
//   - correlationID != "" => correlated AAD（追加 bulkCorrelationId）。
//
// base 与 correlated 的 AAD 不同（correlation 字段在或不在），防止二者偷换。
// 字符串：UInt32 big-endian 长度前缀 + UTF-8；整数：UInt32 big-endian。
func CanonicalChunkAAD(groupID string, index, count uint32, correlationID string) []byte {
	var b bytes.Buffer
	b.WriteString(domain)
	writeU32(&b, Version)
	writeLenStr(&b, groupID)
	writeU32(&b, index)
	writeU32(&b, count)
	if correlationID != "" {
		writeLenStr(&b, correlationID)
	}
	return b.Bytes()
}

func writeU32(b *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	b.Write(tmp[:])
}

func writeLenStr(b *bytes.Buffer, s string) {
	writeU32(b, uint32(len(s)))
	b.WriteString(s)
}
