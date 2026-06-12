package gobridge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	path string
	mem  *MemoryDeviceStore
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

func (s *FileDeviceStore) AddDevice(record TrustedDeviceRecord) error {
	if err := s.mem.AddDevice(record); err != nil {
		return err
	}
	return s.save()
}

func (s *FileDeviceStore) ReplaceDevice(record TrustedDeviceRecord) ([]string, error) {
	replaced, err := s.mem.ReplaceDevice(record)
	if err != nil {
		return nil, err
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	return replaced, nil
}

func (s *FileDeviceStore) LookupByDeviceID(deviceID string) (*TrustedDeviceRecord, error) {
	return s.mem.LookupByDeviceID(deviceID)
}

func (s *FileDeviceStore) LookupByTokenHash(hash string) (*TrustedDeviceRecord, error) {
	return s.mem.LookupByTokenHash(hash)
}

func (s *FileDeviceStore) EnableRelay(deviceID, identityPublicKey string, generation uint64) error {
	if err := s.mem.EnableRelay(deviceID, identityPublicKey, generation); err != nil {
		return err
	}
	return s.save()
}

func (s *FileDeviceStore) RevokeDevice(deviceID string) error {
	if err := s.mem.RevokeDevice(deviceID); err != nil {
		return err
	}
	return s.save()
}

func (s *FileDeviceStore) ListDevices() ([]TrustedDeviceRecord, error) {
	return s.mem.ListDevices()
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

func (s *FileDeviceStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("创建 devices.json 目录失败: %w", err)
	}
	data, err := json.MarshalIndent(s.mem.allDevices(), "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 devices.json 失败: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0o600)
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
