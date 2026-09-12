package gobridge

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// web_push_badge.go — per-device badge state（badge-and-collapse plan §5 B1/B2）。
//
// 口径（B1）：badge = 认证设备自上次成功确认以来，enrollment 期间观察到 completion 的
// 不同 (backendId, sessionId) 数量。按 deviceId 独立持久化；同一会话连续完成只计 1；
// 不读 reducer 投影、不从通知条数或客户端自增猜测。
//
// 持久化：独立于 subscriptions 的 badge-state 文件（同 dataDir，0600 原子写）。文件只
// 保存不可逆 sessionAggregationKey、bindingId 与 revision——不保存标题、正文、sessionId、
// endpoint 或密钥。badge 文件损坏只关闭 badge 表达（fail closed），不把健康订阅判成
// misconfigured（B5）。

const (
	webPushBadgeStateFile = "web-push-badge-state.json"

	// webPushBadgeMaxUnreadSessions：每设备最多保存的 unread session key 数。第 4097 个
	// 不同 key 进入 saturated：revision 继续推进、badge 数字停发（B2）。
	webPushBadgeMaxUnreadSessions = 4096

	// webPushBadgeMaxCount：payload badge 的统一上限（B3，0..999）。
	webPushBadgeMaxCount = 999
)

// webPushBindingIDPattern：wpb_ + 22 个 base64url 字符（客户端 16 random bytes）。
var webPushBindingIDPattern = regexp.MustCompile(`^wpb_[A-Za-z0-9_-]{22}$`)

// IsValidWebPushBindingID 校验 bindingId 固定格式（register params additive 字段）。
func IsValidWebPushBindingID(id string) bool {
	return webPushBindingIDPattern.MatchString(id)
}

// WebPushBadgeDeviceState 是单个设备的 badge 状态（持久化行）。
type WebPushBadgeDeviceState struct {
	BindingID       string           `json:"bindingId"`
	Revision        uint64           `json:"revision"`
	UnreadSessions  map[string]uint64 `json:"unreadSessions"` // sessionAggregationKey → lastCompletionRevision
	Saturated       bool             `json:"saturated"`
	UpdatedAtMillis int64            `json:"updatedAtMillis"`
}

// WebPushBadgeStateFile 是 badge-state 文件的持久化形状。
type WebPushBadgeStateFile struct {
	SchemaVersion int                               `json:"schemaVersion"`
	Devices       map[string]WebPushBadgeDeviceState `json:"devices"` // deviceId → state
}

// WebPushBadgeSnapshot 是投递时注入 payload 的持久化后快照（B2/B3）。
type WebPushBadgeSnapshot struct {
	BindingID string
	Revision  uint64
	Count     int // 1..999；saturated 时调用方不产生快照
}

// loadBadgeStateLocked 读取 badge-state 文件。损坏 → badgeDisabled=true（只关闭 badge，
// 不影响订阅健康度）；不存在 → 空 map（首次启用）。
func (s *WebPushStore) loadBadgeStateLocked() {
	s.badgeDevices = make(map[string]WebPushBadgeDeviceState)
	raw, err := os.ReadFile(filepath.Join(s.dir, webPushBadgeStateFile))
	if err != nil {
		if !os.IsNotExist(err) {
			s.badgeDisabled = true
		}
		return
	}
	var file WebPushBadgeStateFile
	if json.Unmarshal(raw, &file) != nil || file.SchemaVersion != WebPushSchemaVersion {
		s.badgeDisabled = true
		return
	}
	for device, state := range file.Devices {
		if device != "" && state.BindingID != "" {
			if state.UnreadSessions == nil {
				state.UnreadSessions = make(map[string]uint64)
			}
			s.badgeDevices[device] = state
		}
	}
}

// persistBadgeStateLocked 原子写 badge-state 文件；失败返回错误（调用方决定缺省三元组）。
func (s *WebPushStore) persistBadgeStateLocked() error {
	file := WebPushBadgeStateFile{SchemaVersion: WebPushSchemaVersion, Devices: s.badgeDevices}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteWebPush0600(filepath.Join(s.dir, webPushBadgeStateFile), append(raw, '\n'))
}

// RegisterWithBinding 在 Register 语义之上处理 badge binding 生命周期（B2）：
//   - bindingId 为空（旧客户端）：订阅能力不变，不建/不动 badge 状态。
//   - 同 device 同 bindingId：幂等重注册，保留集合；但 subscription identity 改变时
//     拒绝（客户端必须走完整 rebind 并生成新 bindingId）。
//   - 同 device 不同 bindingId：开启新状态并清空旧集合。
//
// subscription identity = endpoint + keys（浏览器 subscription 对象的内容）；disable 后
// 浏览器可能再次给出相同 endpoint，所以仅凭 endpoint 相等不能判定同一启用周期。
func (s *WebPushStore) RegisterWithBinding(deviceID string, record PushSubscriptionRecord, bindingID string) (string, error) {
	if bindingID != "" && !IsValidWebPushBindingID(bindingID) {
		return "", &webPushValidationError{code: WebPushErrInvalidSubscription, message: "bindingId must be wpb_<22 base64url chars>", retryable: false}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != WebPushStoreHealthy {
		return "", &webPushValidationError{code: WebPushErrUnsupported, message: "web push store is not healthy"}
	}
	if bindingID != "" && !s.badgeDisabled {
		if state, ok := s.badgeDevices[deviceID]; ok {
			sameIdentity := state.BindingID == bindingID
			if sameIdentity && s.badgeSubscriptionIdentityLocked(deviceID) != badgeSubscriptionIdentityOf(record) {
				// 同 bindingId 换 subscription identity：拒绝，防止跨启用周期复活旧集合。
				return "", &webPushValidationError{code: WebPushErrBindingMismatch, message: "bindingId already bound to a different subscription; complete a fresh rebind with a new bindingId", retryable: false}
			}
		}
	}
	subscriptionID, err := s.registerLocked(deviceID, record)
	if err != nil {
		return "", err
	}
	if bindingID == "" || s.badgeDisabled {
		return subscriptionID, nil
	}
	if state, ok := s.badgeDevices[deviceID]; ok && state.BindingID == bindingID {
		return subscriptionID, nil // 幂等重注册：保留集合
	}
	s.badgeDevices[deviceID] = WebPushBadgeDeviceState{
		BindingID:       bindingID,
		UnreadSessions:  make(map[string]uint64),
		UpdatedAtMillis: time.Now().UTC().UnixMilli(),
	}
	if err := s.persistBadgeStateLocked(); err != nil {
		// 状态写失败：不阻断注册（订阅能力优先，B5），badge 表达保持关闭直到下次
		// 成功持久化；诊断必须保留，不得静默。
		delete(s.badgeDevices, deviceID)
		slog.Warn("web-push: badge binding persist failed (badge stays off for this binding)",
			"devicePrefix", safeID(deviceID), "error", err.Error())
	}
	return subscriptionID, nil
}

// badgeSubscriptionIdentityLocked 返回该 device 当前订阅的 identity（endpoint+keys）。
func (s *WebPushStore) badgeSubscriptionIdentityLocked(deviceID string) string {
	record, ok := s.byDeviceID[deviceID]
	if !ok {
		return ""
	}
	return badgeSubscriptionIdentityOf(record)
}

func badgeSubscriptionIdentityOf(record PushSubscriptionRecord) string {
	return record.Endpoint + "\x00" + record.P256dh + "\x00" + record.Auth
}

// AdvanceBadgeOnCompletion 在 completion 投递前推进设备状态（B2）：
// 在 store 锁内原子地递增 revision、更新 session key，并持久化；返回注入 payload 的
// 快照。ok=false 表示本次 payload 必须缺省全部 badge 字段（无 binding / badge 关闭 /
// saturated / 持久化失败），通知本身仍照常发送。状态一旦持久化，即使网络投递失败也
// 保留：后续成功 push 携带收敛后的总数。
func (s *WebPushStore) AdvanceBadgeOnCompletion(deviceID, sessionAggregationKey string, nowMillis int64) (WebPushBadgeSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.badgeDisabled {
		return WebPushBadgeSnapshot{}, false
	}
	state, ok := s.badgeDevices[deviceID]
	if !ok || state.BindingID == "" {
		return WebPushBadgeSnapshot{}, false
	}
	state.Revision++
	if _, seen := state.UnreadSessions[sessionAggregationKey]; seen {
		// 已存在 key：无论是否 saturated 都推进其 revision（ack 水位依赖）。
		state.UnreadSessions[sessionAggregationKey] = state.Revision
	} else if len(state.UnreadSessions) < webPushBadgeMaxUnreadSessions {
		state.UnreadSessions[sessionAggregationKey] = state.Revision
	} else {
		// 第 4097 个不同 key：进入 saturated，revision 继续推进但新 key 不入
		// map（有界），badge 数字停发（B2）。
		state.Saturated = true
	}
	state.UpdatedAtMillis = nowMillis
	s.badgeDevices[deviceID] = state
	if err := s.persistBadgeStateLocked(); err != nil {
		// 内存态保留（后续成功投递收敛），但本次 payload 缺省三元组（B5：不得用内存
		// 集合冒充持久化状态）；诊断必须保留。
		slog.Warn("web-push: badge state persist failed (payload omits badge tuple)",
			"devicePrefix", safeID(deviceID), "error", err.Error())
		return WebPushBadgeSnapshot{}, false
	}
	if state.Saturated {
		return WebPushBadgeSnapshot{}, false
	}
	return WebPushBadgeSnapshot{
		BindingID: state.BindingID,
		Revision:  state.Revision,
		Count:     minInt(webPushBadgeMaxCount, len(state.UnreadSessions)),
	}, true
}

// BadgeStateForDevice 返回设备当前 badge 状态的只读快照（get_push_badge_state RPC 用，
// B4-3）。ok=false 表示该设备无 badge 状态（无 binding / badge 关闭）。
func (s *WebPushStore) BadgeStateForDevice(deviceID string) (bindingID string, revision uint64, unreadCount int, saturated bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.badgeDisabled {
		return "", 0, 0, false, false
	}
	state, exists := s.badgeDevices[deviceID]
	if !exists || state.BindingID == "" {
		return "", 0, 0, false, false
	}
	return state.BindingID, state.Revision, len(state.UnreadSessions), state.Saturated, true
}

// AcknowledgeBadgeThroughRevision 处理 acknowledge_push_badge（B4-4）：
//   - available：只删除 lastCompletionRevision <= throughRevision 的条目；全部删空时
//     清除 saturated。
//   - saturated：只有当前 revision 未超过 throughRevision 才能整体清空；否则保持。
//
// 返回 ack 完成后的当前绝对快照。
func (s *WebPushStore) AcknowledgeBadgeThroughRevision(deviceID, bindingID string, throughRevision uint64, nowMillis int64) (WebPushBadgeSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.badgeDisabled {
		return WebPushBadgeSnapshot{}, false
	}
	state, ok := s.badgeDevices[deviceID]
	if !ok || state.BindingID == "" || state.BindingID != bindingID {
		return WebPushBadgeSnapshot{}, false
	}
	if state.Saturated {
		if state.Revision <= throughRevision {
			// 水位稳定：整体清空并退出 saturated。
			state.UnreadSessions = make(map[string]uint64)
			state.Saturated = false
			state.UpdatedAtMillis = nowMillis
		}
		// 水位仍在前进：保持 saturated，页面不动现有角标，等下一轮 get→ack。
	} else {
		for key, rev := range state.UnreadSessions {
			if rev <= throughRevision {
				delete(state.UnreadSessions, key)
			}
		}
		state.UpdatedAtMillis = nowMillis
	}
	s.badgeDevices[deviceID] = state
	if err := s.persistBadgeStateLocked(); err != nil {
		return WebPushBadgeSnapshot{}, false
	}
	if state.Saturated {
		return WebPushBadgeSnapshot{BindingID: state.BindingID, Revision: state.Revision}, false
	}
	return WebPushBadgeSnapshot{
		BindingID: state.BindingID,
		Revision:  state.Revision,
		Count:     minInt(webPushBadgeMaxCount, len(state.UnreadSessions)),
	}, true
}

// deleteBadgeStateLocked 删除设备 badge 状态（unregister/revoke 联动，B2）。
func (s *WebPushStore) deleteBadgeStateLocked(deviceID string) {
	if _, ok := s.badgeDevices[deviceID]; !ok {
		return
	}
	delete(s.badgeDevices, deviceID)
	// 删除失败只影响 badge 收敛（下次 advance 重写），不阻断订阅删除主路径。
	if err := s.persistBadgeStateLocked(); err != nil {
		slog.Warn("web-push: badge state delete persist failed", "devicePrefix", safeID(deviceID), "error", err.Error())
	}
}

// GenerateWebPushBindingID 生成客户端侧 bindingId（服务端测试/诊断用；真实值由
// 浏览器端生成，永不写日志）。
func GenerateWebPushBindingID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate binding id: %w", err)
	}
	return "wpb_" + base64.RawURLEncoding.EncodeToString(buf), nil
}
