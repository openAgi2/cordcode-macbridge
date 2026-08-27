package gobridge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// web_push_store.go — per-bridge VAPID 身份、device subscription store 与 delivery ledger（§5.1/§8.3）。
//
// 数据文件（runtime DataDir 下，0600 原子写）：
//   web-push-vapid.json          P-256 VAPID keypair（私钥永不写日志/出本机）
//   web-push-subscriptions.json  per-device subscription 记录（upsert key = deviceId）
//   web-push-delivery-ledger.json notification key 去重账本（不保存正文）
//
// Fail-closed 语义：key 文件损坏、或 subscription 已存在但 key 丢失 → misconfigured；
// 此时 register/send 关闭，unregister（不依赖私钥）与显式 Reset 仍可恢复。

const (
	webPushVapidFile         = "web-push-vapid.json"
	webPushSubscriptionsFile = "web-push-subscriptions.json"
	webPushLedgerFile        = "web-push-delivery-ledger.json"

	// ledger 上限（条目数 / 保留期）；按成功终态时间清理，清理不影响 subscription store。
	webPushLedgerMaxEntries = 4096
	webPushLedgerRetention  = 24 * time.Hour
)

// WebPushStoreStatus 是 store 的健康状态。
type WebPushStoreStatus string

const (
	WebPushStoreHealthy       WebPushStoreStatus = "healthy"
	WebPushStoreUnconfigured  WebPushStoreStatus = "unconfigured" // 首次启动：key 与 store 均不存在
	WebPushStoreMisconfigured WebPushStoreStatus = "misconfigured"
)

// WebPushVAPIDKeyFile 是 web-push-vapid.json 的持久化形状。
type WebPushVAPIDKeyFile struct {
	SchemaVersion int    `json:"schemaVersion"`
	PrivateKey    string `json:"privateKey"` // base64url (32-byte scalar)
	PublicKey     string `json:"publicKey"`  // base64url (65-byte uncompressed point)
	CreatedAt     int64  `json:"createdAtMillis"`
}

// WebPushSubscriptionsFile 是 subscription store 的持久化形状。
type WebPushSubscriptionsFile struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Subscriptions []PushSubscriptionRecord `json:"subscriptions"`
}

// WebPushLedgerEntry 是 delivery ledger 单条记录（不保存正文）。
type WebPushLedgerEntry struct {
	NotificationKeyHash string `json:"notificationKeyHash"`
	EventID             string `json:"eventId"`
	SubscriptionID      string `json:"subscriptionId"`
	FirstAttemptMillis  int64  `json:"firstAttemptMillis"`
	LastAttemptMillis   int64  `json:"lastAttemptMillis"`
	Status              string `json:"status"` // accepted | temporary_failed | permanent_failed | expired
}

type WebPushLedgerFile struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Entries       []WebPushLedgerEntry `json:"entries"`
}

// WebPushStore 持有 VAPID key、subscription 与 ledger 的内存态 + 持久化。
type WebPushStore struct {
	mu  sync.Mutex
	dir string

	vapid        *WebPushVAPIDKeyFile
	vapidPrivate *ecdsa.PrivateKey
	status       WebPushStoreStatus
	statusDetail string

	// byDeviceID：upsert key = authenticated deviceId。
	byDeviceID map[string]PushSubscriptionRecord

	ledger      map[string]WebPushLedgerEntry // key = NotificationKeyHash
	ledgerDirty bool

	lastResetAt  int64  // 最近一次显式 Reset 的毫秒时间戳；0 = 从未重置
	lastResetErr string // 最近一次 Reset 失败的原因（恢复成功后清空）
}

// LoadWebPushStore 读取/初始化 DataDir 下的 web push 状态。
// 首次启动（key 与 store 均不存在）生成 keypair；任何损坏进入 misconfigured fail closed。
func LoadWebPushStore(dataDir string) (*WebPushStore, error) {
	s := &WebPushStore{
		dir:        dataDir,
		status:     WebPushStoreUnconfigured,
		byDeviceID: make(map[string]PushSubscriptionRecord),
		ledger:     make(map[string]WebPushLedgerEntry),
	}

	// subscriptions
	subPath := filepath.Join(dataDir, webPushSubscriptionsFile)
	subRaw, subErr := os.ReadFile(subPath)
	switch {
	case subErr == nil:
		var file WebPushSubscriptionsFile
		if err := json.Unmarshal(subRaw, &file); err != nil {
			s.status = WebPushStoreMisconfigured
			s.statusDetail = fmt.Sprintf("subscriptions file corrupt: %v", err)
			return s, nil
		}
		for _, record := range file.Subscriptions {
			if record.DeviceID != "" {
				s.byDeviceID[record.DeviceID] = record
			}
		}
	case os.IsNotExist(subErr):
		// 无 store 文件：允许首次建 key。
	default:
		return nil, fmt.Errorf("read web push subscriptions: %w", subErr)
	}

	// vapid key
	vapidPath := filepath.Join(dataDir, webPushVapidFile)
	vapidRaw, vapidErr := os.ReadFile(vapidPath)
	switch {
	case vapidErr == nil:
		var file WebPushVAPIDKeyFile
		if err := json.Unmarshal(vapidRaw, &file); err != nil {
			s.status = WebPushStoreMisconfigured
			s.statusDetail = "vapid key file corrupt"
			return s, nil
		}
		private, privErr := decodeVAPIDPrivateKey(file.PrivateKey, file.PublicKey)
		if privErr != nil {
			s.status = WebPushStoreMisconfigured
			s.statusDetail = fmt.Sprintf("vapid key material invalid: %v", privErr)
			return s, nil
		}
		s.vapid = &file
		s.vapidPrivate = private
		s.status = WebPushStoreHealthy
	case os.IsNotExist(vapidErr):
		if len(s.byDeviceID) > 0 {
			// §5.1：subscription 已存在但 key 丢失 → misconfigured，不得静默换 key。
			s.status = WebPushStoreMisconfigured
			s.statusDetail = "subscriptions exist but vapid key file is missing"
			return s, nil
		}
		if err := s.regenerateVAPIDLocked(); err != nil {
			return nil, fmt.Errorf("generate vapid key: %w", err)
		}
		s.status = WebPushStoreHealthy
	default:
		return nil, fmt.Errorf("read vapid key: %w", vapidErr)
	}

	// ledger（损坏不阻断 push capability——ledger 只是去重优化；读取失败按空账本重启）。
	if ledgerRaw, err := os.ReadFile(filepath.Join(dataDir, webPushLedgerFile)); err == nil {
		var file WebPushLedgerFile
		if json.Unmarshal(ledgerRaw, &file) == nil {
			now := time.Now().UTC().UnixMilli()
			for _, entry := range file.Entries {
				if entry.Status == "accepted" && now-entry.LastAttemptMillis > webPushLedgerRetention.Milliseconds() {
					continue // 过期成功条目直接清理
				}
				s.ledger[entry.NotificationKeyHash] = entry
			}
		}
	}

	return s, nil
}

func decodeVAPIDPrivateKey(privateB64, publicB64 string) (*ecdsa.PrivateKey, error) {
	privBytes, err := DecodeBase64URL(privateB64)
	if err != nil || len(privBytes) != 32 {
		return nil, fmt.Errorf("private key must be 32-byte base64url")
	}
	pubBytes, err := DecodeBase64URL(publicB64)
	if err != nil || !IsUncompressedP256(pubBytes) {
		return nil, fmt.Errorf("public key must be 65-byte uncompressed base64url")
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), pubBytes)
	if x == nil {
		return nil, fmt.Errorf("public key is not a P-256 point")
	}
	private := new(ecdsa.PrivateKey)
	private.D = new(big.Int).SetBytes(privBytes)
	private.PublicKey.Curve = elliptic.P256()
	private.PublicKey.X, private.PublicKey.Y = x, y
	if !private.IsOnCurve(x, y) {
		return nil, fmt.Errorf("key pair does not match")
	}
	return private, nil
}

// regenerateVAPIDLocked 生成新 P-256 keypair 并 0600 原子写。调用方持锁。
// 仅允许在 subscription store 为空时被调用（首建或显式 Reset 之后）。
func (s *WebPushStore) regenerateVAPIDLocked() error {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	pubBytes := elliptic.Marshal(elliptic.P256(), private.PublicKey.X, private.PublicKey.Y)
	file := WebPushVAPIDKeyFile{
		SchemaVersion: WebPushSchemaVersion,
		PrivateKey:    base64.RawURLEncoding.EncodeToString(private.D.FillBytes(make([]byte, 32))),
		PublicKey:     base64.RawURLEncoding.EncodeToString(pubBytes),
		CreatedAt:     time.Now().UTC().UnixMilli(),
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteWebPush0600(filepath.Join(s.dir, webPushVapidFile), append(raw, '\n')); err != nil {
		return err
	}
	s.vapid = &file
	s.vapidPrivate = private
	return nil
}

func atomicWriteWebPush0600(path string, data []byte) error {
	return core.AtomicWriteFile(path, data, 0o600)
}

// Status 返回当前健康状态与诊断细节（不含密钥材料）。
func (s *WebPushStore) Status() (WebPushStoreStatus, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.statusDetail
}

// VapidPublicKey 返回 base64url 公钥；仅 healthy 时非空。
func (s *WebPushStore) VapidPublicKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != WebPushStoreHealthy || s.vapid == nil {
		return ""
	}
	return s.vapid.PublicKey
}

// VapidPrivateKey 返回解码后的私钥（仅 dispatcher 使用；不得写日志）。
func (s *WebPushStore) VapidPrivateKey() *ecdsa.PrivateKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != WebPushStoreHealthy {
		return nil
	}
	return s.vapidPrivate
}

// Register upsert 该 device 的 subscription（原子替换旧记录）。返回 subscriptionId。
func (s *WebPushStore) Register(deviceID string, record PushSubscriptionRecord) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != WebPushStoreHealthy {
		return "", &webPushValidationError{code: WebPushErrUnsupported, message: "web push store is not healthy"}
	}
	subscriptionID := BuildWebPushSubscriptionID(deviceID, record.Endpoint)
	record.SubscriptionID = subscriptionID
	record.DeviceID = deviceID
	now := time.Now().UTC().UnixMilli()
	if existing, ok := s.byDeviceID[deviceID]; ok && existing.SubscriptionID == subscriptionID {
		record.CreatedAt = existing.CreatedAt // 幂等 upsert 保留首注册时间
	}
	record.UpdatedAt = now
	if record.CreatedAt == 0 {
		record.CreatedAt = now
	}
	s.byDeviceID[deviceID] = record
	if err := s.persistSubscriptionsLocked(); err != nil {
		delete(s.byDeviceID, deviceID)
		return "", &webPushValidationError{code: WebPushErrStorageFailed, message: err.Error(), retryable: true}
	}
	return subscriptionID, nil
}

// Unregister 幂等删除当前 device 自己的记录；返回是否删除了记录。
// 不依赖 VAPID 私钥——misconfigured 下仍可调用（恢复路径）。
func (s *WebPushStore) Unregister(deviceID, subscriptionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.byDeviceID[deviceID]
	if !ok {
		return false, nil
	}
	if subscriptionID != "" && existing.SubscriptionID != subscriptionID {
		// 目标 id 不属于该 device 当前记录：幂等成功，但 nothing removed。
		return false, nil
	}
	delete(s.byDeviceID, deviceID)
	if err := s.persistSubscriptionsLocked(); err != nil {
		s.byDeviceID[deviceID] = existing
		return false, &webPushValidationError{code: WebPushErrStorageFailed, message: err.Error(), retryable: true}
	}
	return true, nil
}

// DeleteDevice 撤销/删除 trusted device 时联动删除其 subscription（§5.1/§10）。
func (s *WebPushStore) DeleteDevice(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byDeviceID[deviceID]; !ok {
		return nil
	}
	delete(s.byDeviceID, deviceID)
	if err := s.persistSubscriptionsLocked(); err != nil {
		return err
	}
	slog.Info("web-push: subscription removed with device", "devicePrefix", safeID(deviceID))
	return nil
}

// SubscriptionCount 返回当前 subscription 数（设置页 reset 确认用）。
func (s *WebPushStore) SubscriptionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byDeviceID)
}

// Subscriptions 返回全部记录副本（dispatcher fan-out 用）。
func (s *WebPushStore) Subscriptions() []PushSubscriptionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PushSubscriptionRecord, 0, len(s.byDeviceID))
	for _, record := range s.byDeviceID {
		out = append(out, record)
	}
	return out
}

// ResetWebPush 显式维护动作：清空 subscription store 与 ledger，再重建 key。
// 仅在 store 为空（或调用方明确确认丢弃）后允许重建（§5.1）。
func (s *WebPushStore) ResetWebPush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := len(s.byDeviceID)
	s.byDeviceID = make(map[string]PushSubscriptionRecord)
	s.ledger = make(map[string]WebPushLedgerEntry)
	if err := s.persistSubscriptionsLocked(); err != nil {
		return fmt.Errorf("clear subscriptions: %w", err)
	}
	if err := s.persistLedgerLocked(); err != nil {
		return fmt.Errorf("clear ledger: %w", err)
	}
	if err := s.regenerateVAPIDLocked(); err != nil {
		// store 已清空但 key 重建失败：维持 misconfigured，不得假称恢复。
		s.status = WebPushStoreMisconfigured
		s.statusDetail = fmt.Sprintf("reset key regeneration failed: %v", err)
		s.lastResetAt = time.Now().UTC().UnixMilli()
		s.lastResetErr = err.Error()
		return fmt.Errorf("regenerate vapid key: %w", err)
	}
	s.status = WebPushStoreHealthy
	s.statusDetail = ""
	s.lastResetAt = time.Now().UTC().UnixMilli()
	s.lastResetErr = ""
	slog.Info("web-push: explicit reset completed", "removedSubscriptions", removed)
	return nil
}

// LastResetInfo 返回最近一次显式 Reset 的时间与失败原因（设置页诊断展示）。
func (s *WebPushStore) LastResetInfo() (int64, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastResetAt, s.lastResetErr
}

// MarkSubscriptionExpired 删除过期 subscription（WP-RESP-2 样本门后的 404/410 路径）。
func (s *WebPushStore) MarkSubscriptionExpired(subscriptionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for deviceID, record := range s.byDeviceID {
		if record.SubscriptionID == subscriptionID {
			delete(s.byDeviceID, deviceID)
			return s.persistSubscriptionsLocked()
		}
	}
	return nil
}

// ── delivery ledger（§8.3）───────────────────────────────────────────────────

// LedgerShouldSend 返回该 notification key hash 是否仍应发送（同 key accepted 后不再新发）。
func (s *WebPushStore) LedgerShouldSend(notificationKeyHash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.ledger[notificationKeyHash]
	return !ok || entry.Status != "accepted"
}

// LedgerRecord 记录一次尝试结果。keyHash 为空时忽略（identity 不完整的 candidate 不入账）。
func (s *WebPushStore) LedgerRecord(notificationKeyHash, eventID, subscriptionID, status string) {
	if notificationKeyHash == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().UnixMilli()
	entry, ok := s.ledger[notificationKeyHash]
	if !ok {
		entry = WebPushLedgerEntry{FirstAttemptMillis: now}
	}
	entry.NotificationKeyHash = notificationKeyHash
	entry.EventID = eventID
	entry.SubscriptionID = subscriptionID
	entry.LastAttemptMillis = now
	entry.Status = status
	s.ledger[notificationKeyHash] = entry
	s.ledgerDirty = true
}

// PersistLedgerIfNeeded 持久化账本并执行保留策略清理（锁外 worker 调用）。
func (s *WebPushStore) PersistLedgerIfNeeded() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ledgerDirty {
		return nil
	}
	if len(s.ledger) > webPushLedgerMaxEntries {
		// 按最后尝试时间淘汰最旧的终态条目。
		now := time.Now().UTC().UnixMilli()
		for key, entry := range s.ledger {
			if entry.Status == "accepted" && now-entry.LastAttemptMillis > webPushLedgerRetention.Milliseconds() {
				delete(s.ledger, key)
			}
		}
	}
	if err := s.persistLedgerLocked(); err != nil {
		return err
	}
	s.ledgerDirty = false
	return nil
}

func (s *WebPushStore) persistSubscriptionsLocked() error {
	file := WebPushSubscriptionsFile{SchemaVersion: WebPushSchemaVersion}
	for _, record := range s.byDeviceID {
		file.Subscriptions = append(file.Subscriptions, record)
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteWebPush0600(filepath.Join(s.dir, webPushSubscriptionsFile), append(raw, '\n'))
}

func (s *WebPushStore) persistLedgerLocked() error {
	file := WebPushLedgerFile{SchemaVersion: WebPushSchemaVersion}
	for _, entry := range s.ledger {
		file.Entries = append(file.Entries, entry)
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteWebPush0600(filepath.Join(s.dir, webPushLedgerFile), append(raw, '\n'))
}

// WebPushNotificationKeyHash 计算 notification key 的脱敏账本键。
func WebPushNotificationKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// globalWebPushStore 是进程级引用（main.go 启动时设置）。management API 的设备撤销
// 路径经它联动删除该 device 的 subscription（§10 生命周期不变量：撤销 = 同事务删订阅）。
// dev 模式（无 dataDir）保持 nil。
var globalWebPushStore *WebPushStore
