package gobridge

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"
)

// PairingState 表示配对会话的生命周期状态。
type PairingState string

const (
	PairingCreated   PairingState = "created"
	PairingClaimed   PairingState = "claimed"
	PairingApproved  PairingState = "approved"
	PairingCompleted PairingState = "completed"
	PairingRejected  PairingState = "rejected"
	PairingExpired   PairingState = "expired"
)

// PairingError 是配对操作的结构化错误，携带机器可读的错误码。
type PairingError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e PairingError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// PairingSession 表示一次 QR/手动码 配对会话的完整状态。
type PairingSession struct {
	mu                 sync.Mutex   `json:"-"` // 保护状态变更的互斥锁，序列化时忽略
	ID                 string       `json:"id"`
	QRPayload          string       `json:"qrPayload"`
	ManualCode         string       `json:"manualCode"`
	State              PairingState `json:"state"`
	BridgeID           string       `json:"bridgeId"`
	DisplayName        string       `json:"displayName"`
	LocalURL           string       `json:"localUrl"`
	ClaimingDeviceID   string       `json:"claimingDeviceId,omitempty"`
	ClaimingDeviceName string       `json:"claimingDeviceName,omitempty"`
	ClaimingPlatform   string       `json:"claimingPlatform,omitempty"`
	CreatedAt          time.Time    `json:"createdAt"`
	ExpiresAt          time.Time    `json:"expiresAt"`
	// DeviceToken 仅在 approve 时设置，complete/expire 后清空。
	// 仅用于一次性传输，不持久化到 JSON。
	DeviceToken     string        `json:"-"`
	DeviceTokenHash string        `json:"deviceTokenHash,omitempty"`
	DeviceID        string        `json:"deviceId,omitempty"`
	RelayClaim      *PairingClaim `json:"-"`
}

// pairIDPrefix 是配对会话 ID 的前缀。
const pairIDPrefix = "pair_"

// NewPairingSession 创建一个新的配对会话。
// ttl 决定过期时间距 now 的偏移量。
func NewPairingSession(bridgeID, displayName, localURL, remoteURL string, ttl time.Duration) *PairingSession {
	var remoteURLs []string
	if strings.TrimSpace(remoteURL) != "" {
		remoteURLs = []string{remoteURL}
	}
	return NewPairingSessionWithRemoteURLs(bridgeID, displayName, localURL, remoteURLs, ttl)
}

// NewPairingSessionWithRemoteURLs 创建一个携带多条远程候选地址的配对会话。
func NewPairingSessionWithRemoteURLs(bridgeID, displayName, localURL string, remoteURLs []string, ttl time.Duration) *PairingSession {
	now := time.Now()
	id := pairIDPrefix + generateRandomString(16)
	manualCode := generateManualCode()

	// 从 localURL (ws://host:port) 提取 host 和 port
	qrPayload := fmt.Sprintf("cccode://pair?id=%s&code=%s", id, manualCode)
	if localURL != "" {
		host, port := parseHostPort(localURL)
		if host != "" {
			name := url.QueryEscape(displayName)
			qrPayload = fmt.Sprintf("cccode://pair?id=%s&code=%s&host=%s&port=%s&name=%s",
				id, manualCode, host, port, name)
		}
	}
	// 如果配置了远程 URL，逐个附加到 QR 码，供 iOS 在远程阶段并行尝试。
	for _, remoteURL := range uniqueNonEmptyStrings(remoteURLs) {
		qrPayload += "&remote=" + url.QueryEscape(remoteURL)
	}

	return &PairingSession{
		ID:          id,
		QRPayload:   qrPayload,
		ManualCode:  manualCode,
		State:       PairingCreated,
		BridgeID:    bridgeID,
		DisplayName: displayName,
		LocalURL:    localURL,
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

// Claim 将会话从 Created 转换为 Claimed，记录请求配对的设备信息。
func (s *PairingSession) Claim(deviceID, deviceName, platform string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.IsExpired() {
		return PairingError{Code: "pairing.expired", Message: "配对会话已过期"}
	}
	if s.State != PairingCreated {
		if s.State == PairingClaimed {
			return PairingError{Code: "pairing.already_claimed", Message: "配对会话已被认领"}
		}
		return PairingError{Code: "pairing.invalid_state", Message: fmt.Sprintf("当前状态 %s 不允许认领", s.State)}
	}
	s.State = PairingClaimed
	s.ClaimingDeviceID = deviceID
	s.ClaimingDeviceName = deviceName
	s.ClaimingPlatform = platform
	return nil
}

// Approve 将会话从 Claimed 转换为 Approved，生成设备 token 和 deviceID。
func (s *PairingSession) Approve() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != PairingClaimed {
		return PairingError{Code: "pairing.invalid_state", Message: fmt.Sprintf("当前状态 %s 不允许审批", s.State)}
	}
	plain, hash, err := GenerateDeviceToken()
	if err != nil {
		return fmt.Errorf("生成设备 token 失败: %w", err)
	}
	s.DeviceToken = plain
	s.DeviceTokenHash = hash
	s.DeviceID = s.ClaimingDeviceID
	if s.DeviceID == "" {
		s.DeviceID = "dev_" + generateRandomString(16)
	}
	s.State = PairingApproved
	return nil
}

// Reject 将会话从 Claimed 转换为 Rejected。
func (s *PairingSession) Reject() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != PairingClaimed {
		return PairingError{Code: "pairing.invalid_state", Message: fmt.Sprintf("当前状态 %s 不允许拒绝", s.State)}
	}
	s.State = PairingRejected
	return nil
}

// Complete 将会话从 Approved 转换为 Completed，清空明文 token。
func (s *PairingSession) Complete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != PairingApproved {
		return PairingError{Code: "pairing.invalid_state", Message: fmt.Sprintf("当前状态 %s 不允许完成", s.State)}
	}
	s.State = PairingCompleted
	// 明文 token 已交付，不再保留
	s.DeviceToken = ""
	return nil
}

// IsExpired 检查会话是否已过期。
func (s *PairingSession) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// PairingSessionStore 定义配对会话的持久化接口。
type PairingSessionStore interface {
	Create(session *PairingSession) error
	Get(id string) (*PairingSession, error)
	GetByManualCode(code string) (*PairingSession, error)
	Update(session *PairingSession) error
	DeleteExpired() error
	CleanupAll()
}

// MemoryPairingStore 是基于内存的 PairingSessionStore 实现。
type MemoryPairingStore struct {
	mu           sync.Mutex
	byID         map[string]*PairingSession
	byManualCode map[string]*PairingSession
}

// NewMemoryPairingStore 创建一个空的内存配对会话存储。
func NewMemoryPairingStore() *MemoryPairingStore {
	return &MemoryPairingStore{
		byID:         make(map[string]*PairingSession),
		byManualCode: make(map[string]*PairingSession),
	}
}

// Create 添加一个新会话。如果 ID 已存在则返回错误。
func (s *MemoryPairingStore) Create(session *PairingSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[session.ID]; exists {
		return fmt.Errorf("配对会话 %s 已存在", session.ID)
	}
	s.byID[session.ID] = session
	s.byManualCode[session.ManualCode] = session
	return nil
}

// Get 按 ID 查找会话，找不到返回 nil。
func (s *MemoryPairingStore) Get(id string) (*PairingSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.byID[id]
	if !ok {
		return nil, nil
	}
	return session, nil
}

// GetByManualCode 按 6 位手动码查找会话，找不到返回 nil。
func (s *MemoryPairingStore) GetByManualCode(code string) (*PairingSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.byManualCode[code]
	if !ok {
		return nil, nil
	}
	return session, nil
}

// Update 更新已有会话（按 ID 索引）。
func (s *MemoryPairingStore) Update(session *PairingSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[session.ID]; !exists {
		return fmt.Errorf("配对会话 %s 不存在", session.ID)
	}
	s.byID[session.ID] = session
	s.byManualCode[session.ManualCode] = session
	return nil
}

// DeleteExpired 删除所有已过期的会话，返回删除数量。
func (s *MemoryPairingStore) DeleteExpired() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, session := range s.byID {
		if now.After(session.ExpiresAt) {
			delete(s.byManualCode, session.ManualCode)
			delete(s.byID, id)
		}
	}
	return nil
}

// CleanupAll 清空所有会话（用于运行时重启）。
func (s *MemoryPairingStore) CleanupAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID = make(map[string]*PairingSession)
	s.byManualCode = make(map[string]*PairingSession)
}

// CleanupExpiredSessions 是一个辅助函数，调用 store 的 DeleteExpired。
func CleanupExpiredSessions(store PairingSessionStore) {
	_ = store.DeleteExpired()
}

// generateRandomString 生成指定长度的 URL 安全随机字符串。
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	for i := range result {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[n.Int64()]
	}
	return string(result)
}

// generateManualCode 生成 6 位纯数字的手动配对码。
func generateManualCode() string {
	const digits = "0123456789"
	code := make([]byte, 6)
	for i := range code {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		code[i] = digits[n.Int64()]
	}
	return string(code)
}

// ToJSON 将会话序列化为 JSON（排除 DeviceToken 明文字段）。
func (s *PairingSession) ToJSON() ([]byte, error) {
	return json.Marshal(s)
}

// 确保 PairingSession.QRPayload 中的 ID 和 ManualCode 可被验证。
func init() {
	// 仅用于编译期检查，QRPayload 格式为 cccode://pair?id=<id>&code=<code>
	_ = strings.HasPrefix
}

// parseHostPort 从 ws://host:port 格式的 URL 中提取 host 和 port。
func parseHostPort(rawURL string) (host, port string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", ""
	}
	host = parsed.Hostname()
	port = parsed.Port()
	if port == "" {
		port = "8777"
	}
	return host, port
}
