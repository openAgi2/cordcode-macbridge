package gobridge

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ─── 一次性 Delivery Prekey 池管理 ───────────────────────────────────────
//
// 方案 §5.4：
//   iOS 在线且 channel 已认证时生成一批一次性 X25519 delivery prekey，
//   将 public key 与 prekeyID 经 inner RPC 上传给 Mac；private key 仅存 iOS Keychain。
//   Mac 要向离线设备产生 mailbox 数据时，原子消费一个未使用的 prekeyID，
//   为一个不可追加的 bounded delivery epoch 生成 Mac 临时 X25519 key pair。
//   某设备没有可用 prekey 时，Mac 不以长期 identity key 回退加密详细离线事件。

const (
	prekeyLowWatermark   = 10 // 低水位：未消费 prekey 少于此值触发补充
	prekeyTargetCount    = 32 // 目标水位
	prekeyMaxCount       = 64 // 硬上限
	prekeyLabel          = "cordcode-relay/prekey/v1"
	deliveryEpochLabel   = "cordcode-relay/delivery-epoch/v1"
	macToIosMailboxLabel = "cordcode-relay/mailbox-mac-to-ios/v1"
)

// DeliveryPrekey 记录一个 iOS 上传的一次性 delivery prekey。
type DeliveryPrekey struct {
	PrekeyID   string    `json:"prekeyId"`
	PublicKey  []byte    `json:"publicKey"` // X25519 public key (32 bytes)
	UploadedAt time.Time `json:"uploadedAt"`
	Consumed   bool      `json:"consumed"`
}

// PrekeyUploadBatch 是 iOS 上传的一批 prekey。
// 方案 §5.4：幂等 batch ID，整批接受或拒绝。
type PrekeyUploadBatch struct {
	BatchID  string             `json:"batchId"`
	DeviceID string             `json:"deviceId"`
	Prekeys  []PrekeyUploadItem `json:"prekeys"`
}

// PrekeyUploadItem 单个 prekey 上传项。
type PrekeyUploadItem struct {
	PrekeyID  string `json:"prekeyId"`
	PublicKey string `json:"publicKey"` // base64
}

// PrekeyUploadResponse 上传响应。
// 方案 §5.4 三轮评审：acceptedCount、totalAvailable、duplicateBatchId。
type PrekeyUploadResponse struct {
	AcceptedCount    int    `json:"acceptedCount"`
	TotalAvailable   int    `json:"totalAvailable"`
	DuplicateBatchID bool   `json:"duplicateBatchId"`
	Error            string `json:"-"`
}

// PrekeyStatusResponse prekey 池状态查询响应。
type PrekeyStatusResponse struct {
	AvailableCount int `json:"availableCount"`
	LowWatermark   int `json:"lowWatermark"`
	TargetCount    int `json:"targetCount"`
	MaxCount       int `json:"maxCount"`
	// UrgentRefillNeeded 为 true 表示 Mac 端近期遇到过 prekey 耗尽（availableCount 曾降到 0）
	// 或已跌破低水位。iOS 看到 true 时应立即上传一批 prekey，而不是等下一个 30min 周期。
	// Mac 不主动 push（协议契约不变）；此字段是 iOS 下次 get_delivery_prekey_status 时的 urgency 信号，
	// 读取即清除（消费一次后回到周期补充节奏）。
	UrgentRefillNeeded bool `json:"urgentRefillNeeded"`
}

// DeliveryEpoch 是一个 bounded delivery epoch。
// 方案 §5.4：一个 epoch 只承载本次已聚合的有限批 frame，
// 后续事件必须消费新 prekey 开启新 epoch。
type DeliveryEpoch struct {
	EpochIndex          uint64    `json:"epochIndex"`
	PrekeyID            string    `json:"prekeyId"`
	MacEphemeralPublic  []byte    `json:"macEphemeralPublic"`
	MacEphemeralPrivate []byte    `json:"macEphemeralPrivate,omitempty"` // 仅 Mac 端持有，生成后擦除
	MacToIosMailboxKey  []byte    `json:"macToIosMailboxKey,omitempty"`
	EpochAuthTag        []byte    `json:"epochAuthTag"`
	PreviousEpochDigest string    `json:"previousEpochDigest"`
	FirstCounter        uint64    `json:"firstCounter"`
	LastCounter         uint64    `json:"lastCounter"`
	FrameCount          int       `json:"frameCount"`
	CreatedAt           time.Time `json:"createdAt"`
	Sealed              bool      `json:"sealed"` // epoch 不可追加
}

// DeliveryChainHead 是交付链的链头状态。
type DeliveryChainHead struct {
	EpochIndex            uint64 `json:"epochIndex"`
	EpochDigest           string `json:"epochDigest"`
	LastEpochFinalCounter uint64 `json:"lastEpochFinalCounter"`
	EpochAuthTag          string `json:"epochAuthTag,omitempty"`
	PreviousEpochDigest   string `json:"previousEpochDigest,omitempty"`
}

// PrekeyStore 管理 per-device 的 delivery prekey 池和 delivery epoch chain。
// 部署在 Mac 端 go-bridge 中。
type PrekeyStore struct {
	mu sync.Mutex

	bridgeID string

	// per-device prekey 池
	prekeys map[string][]*DeliveryPrekey // deviceID -> prekeys

	// 已处理的 batch ID，用于幂等
	processedBatches map[string]string // batchID -> deviceID

	// per-device delivery epoch chain
	epochs     map[string][]*DeliveryEpoch // deviceID -> epochs (ordered)
	epochIndex map[string]uint64           // deviceID -> next epoch index

	// identity（用于生成 epochAuthTag）
	identityAuthKey func(deviceID string) ([]byte, error)

	// per-device 紧急补充标记：ConsumePrekey 触发耗尽（availableCount==0）或跌破低水位时置 true，
	// GetPrekeyStatus 读取时清除。解决 Mac 不主动 push + iOS 后台冻结/bridge 重启清零期间的空窗：
	// iOS 重连后第一次 status 查询就能看到 urgency 信号，立即上传一批，不必等 30min 周期。
	urgentRefill map[string]bool
}

// NewPrekeyStore 创建绑定到 bridge identity 的 prekey store。
func NewPrekeyStore(bridgeID string) *PrekeyStore {
	return &PrekeyStore{
		bridgeID:         bridgeID,
		prekeys:          make(map[string][]*DeliveryPrekey),
		processedBatches: make(map[string]string),
		epochs:           make(map[string][]*DeliveryEpoch),
		epochIndex:       make(map[string]uint64),
		urgentRefill:     make(map[string]bool),
	}
}

// SetBridgeID 注入 server 已公布的 bridge identity；启动监听前调用。
func (ps *PrekeyStore) SetBridgeID(bridgeID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.bridgeID = bridgeID
}

// SetIdentityAuthKeyFactory 设置 identity auth key 工厂。
// 延迟绑定，因为 PrekeyStore 创建时可能还没有 crypto identity。
func (ps *PrekeyStore) SetIdentityAuthKeyFactory(fn func(deviceID string) ([]byte, error)) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.identityAuthKey = fn
}

// ── Prekey 上传与查询 ─────────────────────────────────────────────────────

// UploadPrekeys 处理 iOS 上传的一批 delivery prekey。
// 方案 §5.4：幂等 batch ID、硬上限检查、整批接受或拒绝。
func (ps *PrekeyStore) UploadPrekeys(batch PrekeyUploadBatch) PrekeyUploadResponse {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if batch.DeviceID == "" || batch.BatchID == "" || len(batch.Prekeys) == 0 {
		return PrekeyUploadResponse{TotalAvailable: ps.availableCountLocked(batch.DeviceID), Error: "invalid_delivery_prekey_batch"}
	}

	// 幂等检查：相同 batchID 重复上传返回上一次结果
	if existing, ok := ps.processedBatches[batch.BatchID]; ok && existing == batch.DeviceID {
		currentCount := ps.availableCountLocked(batch.DeviceID)
		return PrekeyUploadResponse{
			AcceptedCount:    0, // 已接受过，不再重复
			TotalAvailable:   currentCount,
			DuplicateBatchID: true,
		}
	}
	if _, ok := ps.processedBatches[batch.BatchID]; ok {
		return PrekeyUploadResponse{TotalAvailable: ps.availableCountLocked(batch.DeviceID), Error: "invalid_delivery_prekey_batch"}
	}

	current := ps.availableCountLocked(batch.DeviceID)
	uploadCount := len(batch.Prekeys)

	// 硬上限检查：超过 maxCount 整批拒绝
	if current+uploadCount > prekeyMaxCount {
		return PrekeyUploadResponse{
			AcceptedCount:  0,
			TotalAvailable: current,
			Error:          "prekey_limit_exceeded",
		}
	}

	existingIDs := make(map[string]struct{}, len(ps.prekeys[batch.DeviceID])+len(batch.Prekeys))
	for _, prekey := range ps.prekeys[batch.DeviceID] {
		existingIDs[prekey.PrekeyID] = struct{}{}
	}
	validated := make([]*DeliveryPrekey, 0, len(batch.Prekeys))
	for _, item := range batch.Prekeys {
		if item.PrekeyID == "" {
			return PrekeyUploadResponse{TotalAvailable: current, Error: "invalid_delivery_prekey_batch"}
		}
		if _, exists := existingIDs[item.PrekeyID]; exists {
			return PrekeyUploadResponse{TotalAvailable: current, Error: "invalid_delivery_prekey_batch"}
		}
		pubBytes, err := base64.StdEncoding.DecodeString(item.PublicKey)
		if err != nil {
			return PrekeyUploadResponse{TotalAvailable: current, Error: "invalid_delivery_prekey_batch"}
		}
		if len(pubBytes) != 32 {
			return PrekeyUploadResponse{TotalAvailable: current, Error: "invalid_delivery_prekey_batch"}
		}
		existingIDs[item.PrekeyID] = struct{}{}
		validated = append(validated, &DeliveryPrekey{
			PrekeyID:   item.PrekeyID,
			PublicKey:  pubBytes,
			UploadedAt: time.Now(),
			Consumed:   false,
		})
	}

	// 全批校验成功后才提交，保持 iOS Keychain batch 状态可判定。
	ps.prekeys[batch.DeviceID] = append(ps.prekeys[batch.DeviceID], validated...)
	ps.processedBatches[batch.BatchID] = batch.DeviceID
	accepted := len(validated)
	total := ps.availableCountLocked(batch.DeviceID)

	slog.Info("prekey-store: batch uploaded",
		"deviceID", safeID(batch.DeviceID),
		"batchID", safeID(batch.BatchID),
		"accepted", accepted,
		"totalAvailable", total,
	)

	return PrekeyUploadResponse{
		AcceptedCount:  accepted,
		TotalAvailable: total,
	}
}

// GetPrekeyStatus 返回设备的 prekey 池状态。
// 读取并清除 urgentRefillNeeded：iOS 看到 true 时应立即上传一批 prekey（不等 30min 周期）。
// 消费一次后回到周期补充节奏，避免 urgency 信号长期置位。
func (ps *PrekeyStore) GetPrekeyStatus(deviceID string) PrekeyStatusResponse {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	count := ps.availableCountLocked(deviceID)
	urgent := ps.urgentRefill[deviceID]
	// 实时状态：当前已达标（≥ targetCount）则不报 urgent，即使历史曾耗尽。
	if count >= prekeyTargetCount {
		urgent = false
	}
	delete(ps.urgentRefill, deviceID)
	return PrekeyStatusResponse{
		AvailableCount:     count,
		LowWatermark:       prekeyLowWatermark,
		TargetCount:        prekeyTargetCount,
		MaxCount:           prekeyMaxCount,
		UrgentRefillNeeded: urgent,
	}
}

// ShouldRefill 计算需要补充的 prekey 数量。
// 方案 §5.4：min(targetCount - availableCount, maxCount - availableCount)。
func (ps *PrekeyStore) ShouldRefill(deviceID string) int {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	available := ps.availableCountLocked(deviceID)
	needed := prekeyTargetCount - available
	if needed <= 0 {
		return 0
	}
	capacity := prekeyMaxCount - available
	if capacity < needed {
		needed = capacity
	}
	return needed
}

// ── Prekey 消费与 Delivery Epoch ──────────────────────────────────────────

// ConsumePrekey 原子消费一个未使用的 prekey 并创建 delivery epoch。
// 方案 §5.4：Mac 原子消费一个 prekeyID，为 bounded epoch 生成临时 X25519 key pair，
// 派生 macToIosMailboxKey，生成 epochAuthTag。
//
// 返回创建的 epoch 或错误。如果 prekey 耗尽，返回 prekey_exhausted 错误。
func (ps *PrekeyStore) ConsumePrekey(deviceID string) (*DeliveryEpoch, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.bridgeID == "" {
		return nil, fmt.Errorf("bridge identity not configured")
	}
	if ps.identityAuthKey == nil {
		return nil, fmt.Errorf("identity auth key factory not set")
	}

	// 找到第一个未消费的 prekey
	var prekey *DeliveryPrekey
	prekeyIdx := -1
	for i, pk := range ps.prekeys[deviceID] {
		if !pk.Consumed {
			prekey = pk
			prekeyIdx = i
			break
		}
	}
	if prekey == nil {
		// 耗尽：置 urgent 标记，iOS 下次 status 查询时看到 urgency 信号立即补充。
		ps.urgentRefill[deviceID] = true
		return nil, fmt.Errorf("prekey_exhausted: no available delivery prekey for device %s", safeID(deviceID))
	}

	// 生成 Mac epoch ephemeral key pair
	ephemeralPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate epoch ephemeral: %w", err)
	}

	// 派生 macToIosMailboxKey
	// X25519(macEpochEphemeralPrivateKey, iosDeliveryPrekeyPublicKey)
	iosPub, err := ecdh.X25519().NewPublicKey(prekey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("parse iOS prekey public: %w", err)
	}
	shared, err := ephemeralPriv.ECDH(iosPub)
	if err != nil {
		return nil, fmt.Errorf("prekey ECDH: %w", err)
	}

	// HKDF 派生 mailbox key（两步，与 crypto vectors 一致）
	// mailboxRoot = HKDF(shared, nil, "cordcode-relay/mailbox/v1" + context)
	// macToIosKey = HKDF(mailboxRoot, nil, "mac-to-ios")
	epochIdx := ps.epochIndex[deviceID] // 从 0 开始的递增序号
	context := ps.buildMailboxContext(deviceID, prekey.PrekeyID, epochIdx)
	mailboxRoot, err := hkdfExpand(shared, append([]byte("cordcode-relay/mailbox/v1"), context...), 32)
	if err != nil {
		return nil, fmt.Errorf("derive mailbox root: %w", err)
	}
	mailboxKey, err := hkdfExpand(mailboxRoot, []byte("mac-to-ios"), 32)
	if err != nil {
		return nil, fmt.Errorf("derive mailbox key: %w", err)
	}

	// 获取 previous epoch digest
	previousDigest := ""
	if epochs, ok := ps.epochs[deviceID]; ok && len(epochs) > 0 {
		lastEpoch := epochs[len(epochs)-1]
		if lastEpoch.Sealed {
			digest := ps.computeEpochDigest(lastEpoch)
			previousDigest = base64.StdEncoding.EncodeToString(digest)
		}
	}

	// 生成 epochAuthTag
	// 方案 §5.4：用 identityAuthKey 对 prekeyID、Mac 临时公钥、epochIndex、前序 digest 生成 HMAC。
	epochAuthTag, err := ps.generateEpochAuthTag(deviceID, prekey.PrekeyID, ephemeralPriv.PublicKey().Bytes(), epochIdx, previousDigest)
	if err != nil {
		return nil, fmt.Errorf("generate epoch auth tag: %w", err)
	}

	epoch := &DeliveryEpoch{
		EpochIndex:          epochIdx,
		PrekeyID:            prekey.PrekeyID,
		MacEphemeralPublic:  ephemeralPriv.PublicKey().Bytes(),
		MacEphemeralPrivate: ephemeralPriv.Bytes(),
		MacToIosMailboxKey:  mailboxKey,
		EpochAuthTag:        epochAuthTag,
		PreviousEpochDigest: previousDigest,
		FirstCounter:        1,
		LastCounter:         0, // 无 frame 时为 0
		FrameCount:          0,
		CreatedAt:           time.Now(),
		Sealed:              false,
	}

	// 派生和认证全部成功后才消费，防止错误路径烧掉一次性 prekey。
	ps.prekeys[deviceID][prekeyIdx].Consumed = true
	ps.epochs[deviceID] = append(ps.epochs[deviceID], epoch)
	ps.epochIndex[deviceID] = epochIdx + 1

	// 消费后剩余未消费数跌破低水位 → 置 urgent，提示 iOS 下次查询时立即补充。
	if ps.availableCountLocked(deviceID) < prekeyLowWatermark {
		ps.urgentRefill[deviceID] = true
	}

	slog.Info("prekey-store: epoch created",
		"deviceID", safeID(deviceID),
		"prekeyID", safeID(prekey.PrekeyID),
		"epochIndex", epochIdx,
	)

	return epoch, nil
}

// SealEpoch 密封一个 delivery epoch，擦除临时密钥材料。
// 方案 §5.4：Mac 将该 bounded epoch 的全部密文生成完毕后擦除临时私钥与 batch key。
func (ps *PrekeyStore) SealEpoch(deviceID string, epochIndex uint64, lastCounter uint64, frameCount int) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	epochs, ok := ps.epochs[deviceID]
	if !ok {
		return fmt.Errorf("no epochs for device %s", safeID(deviceID))
	}

	// 找到对应 epoch
	for _, ep := range epochs {
		if ep.EpochIndex == epochIndex && !ep.Sealed {
			ep.Sealed = true
			ep.LastCounter = lastCounter
			ep.FrameCount = frameCount
			// 擦除临时密钥材料
			zeroBytes(ep.MacEphemeralPrivate)
			zeroBytes(ep.MacToIosMailboxKey)
			ep.MacEphemeralPrivate = nil
			ep.MacToIosMailboxKey = nil
			return nil
		}
	}

	return fmt.Errorf("epoch %d not found or already sealed for device %s", epochIndex, safeID(deviceID))
}

// GetDeliveryChainHead 返回设备的交付链头。
// 方案 §5.5：get_delivery_chain_head inner RPC。
func (ps *PrekeyStore) GetDeliveryChainHead(deviceID string) (*DeliveryChainHead, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	epochs, ok := ps.epochs[deviceID]
	if !ok || len(epochs) == 0 {
		return nil, nil // 无 epoch
	}

	// 找到最近一个已密封的 epoch
	for i := len(epochs) - 1; i >= 0; i-- {
		ep := epochs[i]
		if ep.Sealed {
			digest := ps.computeEpochDigest(ep)
			return &DeliveryChainHead{
				EpochIndex:            ep.EpochIndex,
				LastEpochFinalCounter: ep.LastCounter,
				EpochDigest:           base64.StdEncoding.EncodeToString(digest),
				EpochAuthTag:          base64.StdEncoding.EncodeToString(ep.EpochAuthTag),
				PreviousEpochDigest:   ep.PreviousEpochDigest,
			}, nil
		}
	}

	return nil, nil // 无已密封 epoch
}

// GetActiveEpoch 返回设备当前未密封的 epoch（用于添加 frame）。
func (ps *PrekeyStore) GetActiveEpoch(deviceID string) *DeliveryEpoch {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	epochs, ok := ps.epochs[deviceID]
	if !ok || len(epochs) == 0 {
		return nil
	}

	for i := len(epochs) - 1; i >= 0; i-- {
		if !epochs[i].Sealed {
			return epochs[i]
		}
	}
	return nil
}

// ReserveNextFrameCounter 为 active delivery epoch 预留下一个 frame counter。
// 如果当前没有可追加 epoch，会先消费一个 delivery prekey 创建新 epoch。
func (ps *PrekeyStore) ReserveNextFrameCounter(deviceID string) (*DeliveryEpoch, uint64, error) {
	ps.mu.Lock()
	epoch := ps.activeEpochLocked(deviceID)
	ps.mu.Unlock()
	if epoch == nil {
		if _, err := ps.ConsumePrekey(deviceID); err != nil {
			return nil, 0, err
		}
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()
	epoch = ps.activeEpochLocked(deviceID)
	if epoch == nil {
		return nil, 0, fmt.Errorf("no active epoch for device %s", safeID(deviceID))
	}
	counter := epoch.LastCounter + 1
	if counter < epoch.FirstCounter {
		counter = epoch.FirstCounter
	}
	epoch.LastCounter = counter
	epoch.FrameCount++

	copyEpoch := *epoch
	copyEpoch.MacEphemeralPublic = append([]byte(nil), epoch.MacEphemeralPublic...)
	copyEpoch.MacEphemeralPrivate = append([]byte(nil), epoch.MacEphemeralPrivate...)
	copyEpoch.MacToIosMailboxKey = append([]byte(nil), epoch.MacToIosMailboxKey...)
	copyEpoch.EpochAuthTag = append([]byte(nil), epoch.EpochAuthTag...)
	return &copyEpoch, counter, nil
}

func (ps *PrekeyStore) activeEpochLocked(deviceID string) *DeliveryEpoch {
	epochs, ok := ps.epochs[deviceID]
	if !ok || len(epochs) == 0 {
		return nil
	}
	for i := len(epochs) - 1; i >= 0; i-- {
		ep := epochs[i]
		if !ep.Sealed && len(ep.MacToIosMailboxKey) > 0 {
			return ep
		}
	}
	return nil
}

// RemoveConsumedPrekeys 清理已消费且 epoch 已密封的 prekey。
func (ps *PrekeyStore) RemoveConsumedPrekeys(deviceID string) int {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	prekeys := ps.prekeys[deviceID]
	remaining := prekeys[:0]
	removed := 0
	for _, pk := range prekeys {
		if pk.Consumed {
			removed++
		} else {
			remaining = append(remaining, pk)
		}
	}
	ps.prekeys[deviceID] = remaining
	return removed
}

// ── 内部方法 ─────────────────────────────────────────────────────────────

func (ps *PrekeyStore) availableCountLocked(deviceID string) int {
	count := 0
	for _, pk := range ps.prekeys[deviceID] {
		if !pk.Consumed {
			count++
		}
	}
	return count
}

func (ps *PrekeyStore) buildMailboxContext(deviceID, prekeyID string, epochIndex uint64) []byte {
	// 与 crypto vectors 中的 contextCanonical 格式一致
	ctx := []byte(fmt.Sprintf(`["%s","%s","%s",%d]`, ps.bridgeID, deviceID, prekeyID, epochIndex))
	return ctx
}

func (ps *PrekeyStore) generateEpochAuthTag(deviceID, prekeyID string, macEphemeralPublic []byte, epochIndex uint64, previousDigest string) ([]byte, error) {
	// 获取 identity auth key
	if ps.identityAuthKey == nil {
		return nil, fmt.Errorf("identity auth key factory not set")
	}
	authKey, err := ps.identityAuthKey(deviceID)
	if err != nil {
		return nil, fmt.Errorf("get identity auth key: %w", err)
	}

	// 构造 epoch header 用于 HMAC
	// 方案 §5.4：对 prekeyID、Mac 临时公钥、epochIndex、前序 digest 生成 epochAuthTag
	header := map[string]interface{}{
		"prekeyId":              prekeyID,
		"macEphemeralPublicKey": base64.StdEncoding.EncodeToString(macEphemeralPublic),
		"epochIndex":            epochIndex,
		"previousEpochDigest":   previousDigest,
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshal epoch header: %w", err)
	}

	return hmacSHA256(authKey, headerJSON), nil
}

func (ps *PrekeyStore) computeEpochDigest(epoch *DeliveryEpoch) []byte {
	// 构造 epoch header 的规范形式（与 crypto vectors epochHeaderCanonical 一致）
	header := map[string]interface{}{
		"epochIndex":            epoch.EpochIndex,
		"firstCounter":          epoch.FirstCounter,
		"frameCount":            epoch.FrameCount,
		"lastCounter":           epoch.LastCounter,
		"macEphemeralPublicKey": base64.StdEncoding.EncodeToString(epoch.MacEphemeralPublic),
		"prekeyId":              epoch.PrekeyID,
		"previousEpochDigest":   epoch.PreviousEpochDigest,
	}
	headerJSON, _ := json.Marshal(header)
	// epochDigest = SHA256(headerJSON + epochAuthTag)，与 crypto vectors 一致
	digestInput := append(headerJSON, epoch.EpochAuthTag...)
	h := sha256.Sum256(digestInput)
	return h[:]
}

// ── Mailbox 密钥派生辅助（iOS 端使用） ────────────────────────────────────

// DeriveMailboxKeyFromPrekey 从 iOS 端角度派生 mailbox key。
// iOS 使用自己的 prekey private key 和 Mac 的 epoch ephemeral public key 派生相同密钥。
func DeriveMailboxKeyFromPrekey(
	prekeyPrivate *ecdh.PrivateKey,
	macEphemeralPublic []byte,
	bridgeID, deviceID, prekeyID string,
	epochIndex uint64,
) ([]byte, error) {
	macPub, err := ecdh.X25519().NewPublicKey(macEphemeralPublic)
	if err != nil {
		return nil, fmt.Errorf("parse Mac ephemeral public: %w", err)
	}

	shared, err := prekeyPrivate.ECDH(macPub)
	if err != nil {
		return nil, fmt.Errorf("prekey ECDH: %w", err)
	}

	// 两步 HKDF，与 Mac 端 ConsumePrekey 一致
	if bridgeID == "" {
		return nil, fmt.Errorf("bridge identity not configured")
	}
	ctx := []byte(fmt.Sprintf(`["%s","%s","%s",%d]`, bridgeID, deviceID, prekeyID, epochIndex))
	mailboxRoot, err := hkdfExpand(shared, append([]byte("cordcode-relay/mailbox/v1"), ctx...), 32)
	if err != nil {
		return nil, fmt.Errorf("derive mailbox root: %w", err)
	}
	return hkdfExpand(mailboxRoot, []byte("mac-to-ios"), 32)
}

// VerifyEpochAuthTag 验证 epoch auth tag。
// iOS 使用 identity auth key 验证 Mac 生成的 auth tag。
func VerifyEpochAuthTag(
	identityAuthKey []byte,
	prekeyID string,
	macEphemeralPublic []byte,
	epochIndex uint64,
	previousEpochDigest string,
	expectedTag []byte,
) bool {
	header := map[string]interface{}{
		"prekeyId":              prekeyID,
		"macEphemeralPublicKey": base64.StdEncoding.EncodeToString(macEphemeralPublic),
		"epochIndex":            epochIndex,
		"previousEpochDigest":   previousEpochDigest,
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return false
	}

	computed := hmacSHA256(identityAuthKey, headerJSON)
	return hmac.Equal(computed, expectedTag)
}

// VerifyDeliveryChain 验证 delivery chain 完整性。
// 检查 epoch chain 的 previousEpochDigest 链接是否完整。
func VerifyDeliveryChain(chainHeads []*DeliveryChainHead) bool {
	if len(chainHeads) == 0 {
		return true
	}

	// 按 epochIndex 排序后检查 previousEpochDigest 链接
	for i := 1; i < len(chainHeads); i++ {
		prev := chainHeads[i-1]
		curr := chainHeads[i]
		if curr.PreviousEpochDigest != prev.EpochDigest {
			return false
		}
	}
	return true
}

// ── 编码辅助 ────────────────────────────────────────────────────────────

// EncodeUint64BE 将 uint64 编码为 8 字节大端序。
func EncodeUint64BE(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}
