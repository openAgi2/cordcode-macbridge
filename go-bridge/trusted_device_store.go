package gobridge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/openAgi2/cordcode-macbridge/core"
	"os"
	"sync"
	"time"
)

// TrustedDeviceRecord 存储一台已信任设备的完整信息。
type TrustedDeviceRecord struct {
	DeviceID               string     `json:"deviceId"`
	DisplayName            string     `json:"displayName"`
	Platform               string     `json:"platform"`
	TokenHash              string     `json:"tokenHash"`
	IdentityPublicKey      string     `json:"identityPublicKey,omitempty"`
	RelayEnabled           bool       `json:"relayEnabled,omitempty"`
	RelayChannelGeneration uint64     `json:"relayChannelGeneration,omitempty"`
	CreatedAt              time.Time  `json:"createdAt"`
	LastSeenAt             time.Time  `json:"lastSeenAt"`
	LastRemoteAddress      string     `json:"lastRemoteAddress,omitempty"`
	RevokedAt              *time.Time `json:"revokedAt,omitempty"`
}

// Clone 返回记录的深拷贝。*time.Time 指针字段必须单独复制，否则新旧快照
// 共享同一指针，后续对原 store 的 RevokeDevice 会污染副本（P1-3 深拷贝陷阱）。
func (r TrustedDeviceRecord) Clone() TrustedDeviceRecord {
	out := r
	if r.RevokedAt != nil {
		t := *r.RevokedAt
		out.RevokedAt = &t
	}
	return out
}

// TrustedDeviceStore 定义设备存储的抽象接口。
type TrustedDeviceStore interface {
	AddDevice(record TrustedDeviceRecord) error
	ReplaceDevice(record TrustedDeviceRecord) ([]string, error)
	LookupByDeviceID(deviceID string) (*TrustedDeviceRecord, error)
	LookupByTokenHash(hash string) (*TrustedDeviceRecord, error)
	EnableRelay(deviceID, identityPublicKey string, generation uint64) error
	RevokeDevice(deviceID string) error
	ListDevices() ([]TrustedDeviceRecord, error)
}

// ReplaceDevice 保存最新配对凭据，并删除同一设备的旧记录。
// 新版客户端使用稳定 deviceID；DisplayName + Platform 用于清理升级前的随机 ID 记录。
func (s *MemoryDeviceStore) ReplaceDevice(record TrustedDeviceRecord) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	replaced := make([]string, 0)
	for deviceID, existing := range s.byID {
		sameDeviceID := deviceID == record.DeviceID
		sameLegacyIdentity := existing.Platform == record.Platform &&
			existing.DisplayName == record.DisplayName
		if !sameDeviceID && !sameLegacyIdentity {
			continue
		}
		delete(s.byID, deviceID)
		delete(s.byToken, existing.TokenHash)
		if deviceID != record.DeviceID {
			replaced = append(replaced, deviceID)
		}
	}
	s.byID[record.DeviceID] = record
	s.byToken[record.TokenHash] = record.DeviceID
	return replaced, nil
}

// MemoryDeviceStore 是基于内存的 TrustedDeviceStore 实现，用于测试。
type MemoryDeviceStore struct {
	mu      sync.RWMutex
	byID    map[string]TrustedDeviceRecord
	byToken map[string]string // tokenHash → deviceID
}

// NewMemoryDeviceStore 创建一个空的内存设备存储。
func NewMemoryDeviceStore() *MemoryDeviceStore {
	return &MemoryDeviceStore{
		byID:    make(map[string]TrustedDeviceRecord),
		byToken: make(map[string]string),
	}
}

// cloneSnapshot 返回当前状态的深拷贝（独立 map + 每条记录 Clone）。
// 调用方必须已持有读锁。用于 FileDeviceStore 的事务性提交：先在副本上变更，
// 原子写盘成功后再一次性替换 mem 指针（P1-3）。
func (s *MemoryDeviceStore) cloneSnapshot() *MemoryDeviceStore {
	out := &MemoryDeviceStore{
		byID:    make(map[string]TrustedDeviceRecord, len(s.byID)),
		byToken: make(map[string]string, len(s.byToken)),
	}
	for id, rec := range s.byID {
		cloned := rec.Clone()
		out.byID[id] = cloned
		out.byToken[rec.TokenHash] = id
	}
	return out
}

// Clone 返回当前 store 的深拷贝快照，供外部（如 FileDeviceStore 提交流程）使用。
func (s *MemoryDeviceStore) Clone() *MemoryDeviceStore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cloneSnapshot()
}

// snapshotRecords 返回所有记录的独立副本切片。
func (s *MemoryDeviceStore) snapshotRecords() []TrustedDeviceRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TrustedDeviceRecord, 0, len(s.byID))
	for _, rec := range s.byID {
		out = append(out, rec.Clone())
	}
	return out
}

// AddDevice 添加一条设备记录。如果 deviceID 已存在则返回错误。
func (s *MemoryDeviceStore) AddDevice(record TrustedDeviceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[record.DeviceID]; exists {
		return fmt.Errorf("设备 %s 已存在", record.DeviceID)
	}
	s.byID[record.DeviceID] = record
	s.byToken[record.TokenHash] = record.DeviceID
	return nil
}

// LookupByDeviceID 按 deviceID 查找设备，找不到返回 nil。
func (s *MemoryDeviceStore) LookupByDeviceID(deviceID string) (*TrustedDeviceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.byID[deviceID]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

// LookupByTokenHash 按 token 哈希查找设备，找不到返回 nil。
func (s *MemoryDeviceStore) LookupByTokenHash(hash string) (*TrustedDeviceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	deviceID, ok := s.byToken[hash]
	if !ok {
		return nil, nil
	}
	rec := s.byID[deviceID]
	return &rec, nil
}

// EnableRelay 将经过现有 token 认证的设备绑定到不可替换的 relay identity key。
func (s *MemoryDeviceStore) EnableRelay(deviceID, identityPublicKey string, generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[deviceID]
	if !ok {
		return fmt.Errorf("设备 %s 不存在", deviceID)
	}
	if rec.RevokedAt != nil {
		return fmt.Errorf("设备 %s 已被吊销", deviceID)
	}
	if rec.IdentityPublicKey != "" && rec.IdentityPublicKey != identityPublicKey {
		return fmt.Errorf("设备 %s 的 identity public key 已绑定", deviceID)
	}
	rec.IdentityPublicKey = identityPublicKey
	rec.RelayEnabled = true
	rec.RelayChannelGeneration = generation
	s.byID[deviceID] = rec
	return nil
}

// RevokeDevice 将设备标记为已吊销。
func (s *MemoryDeviceStore) RevokeDevice(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[deviceID]
	if !ok {
		return fmt.Errorf("设备 %s 不存在", deviceID)
	}
	now := time.Now()
	rec.RevokedAt = &now
	s.byID[deviceID] = rec
	return nil
}

// ListDevices 返回未被吊销的设备记录。
func (s *MemoryDeviceStore) ListDevices() ([]TrustedDeviceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]TrustedDeviceRecord, 0, len(s.byID))
	for _, rec := range s.byID {
		if rec.RevokedAt != nil {
			continue
		}
		result = append(result, rec)
	}
	return result, nil
}

func (s *MemoryDeviceStore) allDevices() []TrustedDeviceRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]TrustedDeviceRecord, 0, len(s.byID))
	for _, rec := range s.byID {
		result = append(result, rec)
	}
	return result
}

// FileDeviceStore 将受信设备持久化到 data-dir/devices.json。
// Product 模式下必须跨 runtime 重启保留 device token，否则 iOS 每次重启后都会 auth.invalid_token。
type FileDeviceStore struct {
	path     string
	commitMu sync.Mutex // 串行化整个 read-modify-write 提交序列（P1-3 并发 lost-update 修复）
	memMu    sync.RWMutex
	mem      *MemoryDeviceStore
}

func NewFileDeviceStore(path string) (*FileDeviceStore, error) {
	store := &FileDeviceStore{
		path: path,
		mem:  NewMemoryDeviceStore(),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// snapshotMem 返回当前内存快照（在 memMu 读锁下读取 mem 指针）。
func (s *FileDeviceStore) snapshotMem() *MemoryDeviceStore {
	s.memMu.RLock()
	defer s.memMu.RUnlock()
	return s.mem
}

// commit 在一个事务里完成“深拷贝 → 修改副本 → 原子写盘 → swap 内存指针”。
// 写盘失败时丢弃副本，内存快照保持旧状态（P1-3）。
//
// 并发正确性（P1-3 review 修复）：commitMu 串行化整个 read-modify-write 序列。
// memMu 只保证单次 mem 指针 swap 的原子性，不保证“读取快照→克隆→改副本→写盘→swap”
// 这一整段序列的隔离性。两个并发 commit 若都基于同一快照克隆，后 swap 者会覆盖先写者，
// 导致先提交的设备记录丢失（经典原子性≠隔离性）。因此整个序列必须在 commitMu 下进行：
// 只读查找走 memMu RLock（不阻塞读），但任意时刻只有一个 commit 在推进。
// fn 在已锁定的独立副本上运行变更逻辑；fn 返回非 nil 错误时不会写盘也不会 swap。
func (s *FileDeviceStore) commit(fn func(clone *MemoryDeviceStore) error) error {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()

	current := s.snapshotMem()
	current.mu.RLock()
	clone := current.cloneSnapshot()
	current.mu.RUnlock()

	if err := fn(clone); err != nil {
		return err
	}

	data, err := json.MarshalIndent(clone.snapshotRecords(), "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 devices.json 失败: %w", err)
	}
	data = append(data, '\n')
	if err := core.AtomicWriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("原子写入 devices.json 失败: %w", err)
	}

	s.memMu.Lock()
	s.mem = clone
	s.memMu.Unlock()
	return nil
}

// AddDevice 添加设备并事务性提交。写盘失败时内存保持旧状态，不会出现“已接受但未持久化”。
func (s *FileDeviceStore) AddDevice(record TrustedDeviceRecord) error {
	return s.commit(func(clone *MemoryDeviceStore) error {
		return clone.AddDevice(record)
	})
}

// ReplaceDevice 保存最新配对凭据并事务性提交。
func (s *FileDeviceStore) ReplaceDevice(record TrustedDeviceRecord) ([]string, error) {
	var replaced []string
	err := s.commit(func(clone *MemoryDeviceStore) error {
		var innerErr error
		replaced, innerErr = clone.ReplaceDevice(record)
		return innerErr
	})
	if err != nil {
		return nil, err
	}
	return replaced, nil
}

func (s *FileDeviceStore) LookupByDeviceID(deviceID string) (*TrustedDeviceRecord, error) {
	return s.snapshotMem().LookupByDeviceID(deviceID)
}

func (s *FileDeviceStore) LookupByTokenHash(hash string) (*TrustedDeviceRecord, error) {
	return s.snapshotMem().LookupByTokenHash(hash)
}

// EnableRelay 绑定 relay identity 并事务性提交。
func (s *FileDeviceStore) EnableRelay(deviceID, identityPublicKey string, generation uint64) error {
	return s.commit(func(clone *MemoryDeviceStore) error {
		return clone.EnableRelay(deviceID, identityPublicKey, generation)
	})
}

// RevokeDevice 吊销设备并事务性提交。写盘失败时内存仍可见为未吊销。
func (s *FileDeviceStore) RevokeDevice(deviceID string) error {
	return s.commit(func(clone *MemoryDeviceStore) error {
		return clone.RevokeDevice(deviceID)
	})
}

func (s *FileDeviceStore) ListDevices() ([]TrustedDeviceRecord, error) {
	return s.snapshotMem().ListDevices()
}

func (s *FileDeviceStore) load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 devices.json 失败: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var records []TrustedDeviceRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("解析 devices.json 失败: %w", err)
	}
	for _, rec := range records {
		s.mem.byID[rec.DeviceID] = rec
		s.mem.byToken[rec.TokenHash] = rec.DeviceID
	}
	return nil
}

// AuthError 表示设备认证失败的结构化错误。
type AuthError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e AuthError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ValidateDeviceAuth 验证设备 token 的完整认证流程。
// 依次检查: token 非空 → token 格式合法 → 哈希查找到设备 → 设备未吊销。
func ValidateDeviceAuth(store TrustedDeviceStore, token string, deviceID string) (*TrustedDeviceRecord, error) {
	// 检查 token 非空
	if token == "" {
		return nil, AuthError{Code: "auth.missing_token", Message: "缺少 device token"}
	}

	// 校验 token 格式
	if err := ValidateTokenPrefix(token); err != nil {
		return nil, AuthError{Code: "auth.invalid_token", Message: "token 格式无效"}
	}

	// 计算 token 哈希并查找设备
	tokenHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(token)))
	rec, err := store.LookupByTokenHash(tokenHash)
	if err != nil {
		return nil, AuthError{Code: "auth.invalid_token", Message: "查询 token 失败"}
	}
	if rec == nil {
		return nil, AuthError{Code: "auth.invalid_token", Message: "未找到匹配的设备"}
	}

	// 如果传了 deviceID，额外校验一致性
	if deviceID != "" && rec.DeviceID != deviceID {
		return nil, AuthError{Code: "auth.invalid_token", Message: "deviceID 与 token 不匹配"}
	}

	// 检查是否已吊销
	if rec.RevokedAt != nil {
		return nil, AuthError{Code: "auth.revoked", Message: "设备已被吊销"}
	}

	return rec, nil
}
