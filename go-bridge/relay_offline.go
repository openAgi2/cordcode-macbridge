package gobridge

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// ─── 离线集成：事件到 outbox/mailbox 的桥接 ──────────────────────────────
//
// 方案 §9 / §15.6：
//   贯通 Codex/OpenCode/Claude 的 relay offline milestones、
//   history/Todo reconcile 与各 backend 真实进程模型差异。
//
// RelayEventRouter 在事件链路中介入，将事件路由到：
//   1. 在线直连设备（现有 Connection.SendEvent）
//   2. Outbox（Mac→Relay 断链时缓冲）
//   3. Mailbox（离线设备的密文暂存）
//
// 决策由 ObservationManager 控制 scope，PrekeyStore 控制 epoch。

// RelayEventRouter 事件路由器。
type RelayEventRouter struct {
	mu sync.RWMutex

	observation         *ObservationManager
	outbox              *OutboxManager
	prekeys             *PrekeyStore
	mailbox             *MailboxService
	presentation        *PresentationManager
	routeID             string
	generationForDevice func(deviceID string) uint64
}

// NewRelayEventRouter 创建事件路由器。
func NewRelayEventRouter(
	observation *ObservationManager,
	outbox *OutboxManager,
	prekeys *PrekeyStore,
	mailbox *MailboxService,
	presentation *PresentationManager,
) *RelayEventRouter {
	return &RelayEventRouter{
		observation:  observation,
		outbox:       outbox,
		prekeys:      prekeys,
		mailbox:      mailbox,
		presentation: presentation,
	}
}

func (rer *RelayEventRouter) SetRouteID(routeID string) {
	rer.mu.Lock()
	defer rer.mu.Unlock()
	rer.routeID = routeID
}

func (rer *RelayEventRouter) SetDeviceGenerationFunc(fn func(deviceID string) uint64) {
	rer.mu.Lock()
	defer rer.mu.Unlock()
	rer.generationForDevice = fn
}

// RouteEvent 将事件路由到合适的投递通道。
// 返回：发送给在线设备的信封（nil 如果不需要在线发送）。
//
// 方案 §8.3：
//
//	Mac 端必须决定向哪些设备发送哪些事件。
//	full_stream 发全部事件；milestones_only 只发 durable milestones。
func (rer *RelayEventRouter) RouteStampedEvent(
	eventMsg EventMessage,
	connectedDevices []string, // 当前在线的设备 ID 列表
	offlineDevices []string, // 需要离线投递的设备 ID 列表
) {
	eventJSON, err := json.Marshal(eventMsg)
	if err != nil {
		slog.Error("relay-router: marshal event", "error", err)
		return
	}

	isDurable := IsDurableMilestone(eventMsg.Event)

	// 在线设备：根据 scope 决定是否发送
	for _, deviceID := range connectedDevices {
		shouldSend := rer.observation.ShouldSendEvent(deviceID, eventMsg.BackendID, eventMsg.SessionID, eventMsg.Event)
		if !shouldSend {
			continue
		}
		// 在线发送由调用方（handlers.go）通过 Connection.SendEvent 处理
		// 这里只做决策记录
		slog.Debug("relay-router: online event",
			"deviceID", safeID(deviceID),
			"event", eventMsg.Event,
			"durable", isDurable,
		)
	}

	// 离线设备：只投递 durable milestones
	if isDurable {
		for _, deviceID := range offlineDevices {
			rer.routeOfflineEvent(deviceID, eventMsg.SessionID, eventMsg.BackendID, eventMsg.Event, eventJSON)
		}
	}
}

// routeOfflineEvent 将 durable milestone 路由到离线设备的 outbox/mailbox。
func (rer *RelayEventRouter) routeOfflineEvent(
	deviceID, sessionID, backendID, eventName string,
	eventJSON []byte,
) {
	rer.mu.RLock()
	routeID := rer.routeID
	generationForDevice := rer.generationForDevice
	rer.mu.RUnlock()
	if routeID == "" {
		slog.Warn("relay-router: route not configured", "deviceID", safeID(deviceID))
		rer.presentation.MarkPendingSync(deviceID, backendID)
		return
	}
	channelGeneration := uint64(0)
	if generationForDevice != nil {
		channelGeneration = generationForDevice(deviceID)
	}
	if channelGeneration == 0 {
		slog.Warn("relay-router: device generation not configured", "deviceID", safeID(deviceID))
		rer.presentation.MarkPendingSync(deviceID, backendID)
		return
	}

	epoch, counter, err := rer.prekeys.ReserveNextFrameCounter(deviceID)
	if err != nil {
		slog.Warn("relay-router: prekey exhausted",
			"deviceID", safeID(deviceID),
			"error", err,
		)
		rer.presentation.MarkPendingSync(deviceID, backendID)
		return
	}

	now := time.Now().UTC()
	epochIndex := epoch.EpochIndex
	epochEphemeral := base64.StdEncoding.EncodeToString(epoch.MacEphemeralPublic)
	epochAuthTag := base64.StdEncoding.EncodeToString(epoch.EpochAuthTag)
	envelope := &RelayEnvelope{
		Version:                 1,
		RouteID:                 routeID,
		SenderID:                "bridge",
		DestinationID:           deviceID,
		ChannelGeneration:       channelGeneration,
		KeyEpochID:              "mailbox:" + strconv.FormatUint(epoch.EpochIndex, 10),
		PrekeyID:                strPtrOffline(epoch.PrekeyID),
		EpochIndex:              &epochIndex,
		EpochEphemeralPublicKey: &epochEphemeral,
		PreviousEpochDigest:     &epoch.PreviousEpochDigest,
		EpochAuthTag:            &epochAuthTag,
		MessageID:               generateRelayID("msg_"),
		Counter:                 counter,
		CreatedAt:               now.Format(time.RFC3339),
		ExpiresAt:               now.Add(defaultMailboxTTL).Format(time.RFC3339),
	}
	aad, err := envelope.EncodeAAD()
	if err != nil {
		slog.Error("relay-router: encode aad", "deviceID", safeID(deviceID), "error", err)
		return
	}
	ciphertext, err := SealEnvelope(epoch.MacToIosMailboxKey, counter, aad, eventJSON)
	if err != nil {
		slog.Error("relay-router: seal envelope", "deviceID", safeID(deviceID), "error", err)
		return
	}
	envelope.Ciphertext = ciphertext

	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		slog.Error("relay-router: marshal envelope", "error", err)
		return
	}

	// 尝试入队到 outbox（Mac→Relay 断链缓冲）
	err = rer.outbox.Enqueue(deviceID, counter, envelopeJSON)
	if err != nil {
		// Outbox 溢出：标记 reconcile
		slog.Warn("relay-router: outbox overflow",
			"deviceID", safeID(deviceID),
			"error", err,
		)
		return
	}

	slog.Debug("relay-router: offline event queued",
		"deviceID", safeID(deviceID),
		"event", eventName,
		"epochIndex", epoch.EpochIndex,
		"counter", counter,
	)
}

// ShouldSendToOnlineDevice 判断是否应向在线设备发送事件。
// 供 handlers.go 调用的便捷方法。
func (rer *RelayEventRouter) ShouldSendToOnlineDevice(
	deviceID, backendID, sessionID, eventName string,
) bool {
	return rer.observation.ShouldSendEvent(deviceID, backendID, sessionID, eventName)
}

func strPtrOffline(s string) *string { return &s }
