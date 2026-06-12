package gobridge

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// globalPairingStore and globalDeviceStore are set from main.go during startup.
var globalPairingStore PairingSessionStore
var globalDeviceStore TrustedDeviceStore

// PairingCompletePush is sent to the iOS device after Mac approves.
type PairingCompletePush struct {
	Type   string                `json:"type"`
	Device PairingCompleteDevice `json:"device"`
	Bridge PairingCompleteBridge `json:"bridge"`
}

type PairingCompleteDevice struct {
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
}

type PairingCompleteBridge struct {
	BridgeID    string   `json:"bridgeId"`
	DisplayName string   `json:"displayName"`
	LocalURL    string   `json:"localURL"`
	RemoteURL   *string  `json:"remoteURL,omitempty"`
	RemoteURLs  []string `json:"remoteURLs,omitempty"`
}

// PairingPendingConn tracks an iOS device waiting for pairing_complete.
type PairingPendingConn struct {
	writeMu   sync.Mutex
	pairingID string
	conn      *websocket.Conn
	done      chan struct{}
}

// PairingPendingRegistry tracks iOS devices waiting for pairing completion.
type PairingPendingRegistry struct {
	mu    sync.Mutex
	conns map[string]*PairingPendingConn
}

var globalPairingRegistry = &PairingPendingRegistry{
	conns: make(map[string]*PairingPendingConn),
}

func (r *PairingPendingRegistry) Register(pairingID string, conn *PairingPendingConn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.conns[pairingID]; exists {
		return false // 已有 pending 连接注册，拒绝重复
	}
	r.conns[pairingID] = conn
	return true
}

func (r *PairingPendingRegistry) Unregister(pairingID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, pairingID)
}

func (r *PairingPendingRegistry) NotifyComplete(pairingID string, push PairingCompletePush) bool {
	r.mu.Lock()

	conn, ok := r.conns[pairingID]
	if !ok {
		r.mu.Unlock()
		return false
	}

	conn.writeMu.Lock()
	defer conn.writeMu.Unlock()

	data, _ := json.Marshal(push)
	if err := conn.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		slog.Warn("pairing_complete 写入失败", "pairingId", pairingID, "error", err)
		close(conn.done)
		delete(r.conns, pairingID)
		r.mu.Unlock()
		return false
	}
	// 释放锁后等待 200ms，让 WriteMessage 的数据从 Go 缓冲区刷到 TCP。
	// 立即触发 defer conn.Close() 发出 RST。
	r.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	r.mu.Lock()
	close(conn.done)
	delete(r.conns, pairingID)
	r.mu.Unlock()
	return true
}

var pairingUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handlePairingWebSocket handles /pairing WebSocket connections from iOS.
func handlePairingWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := pairingUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("pairing websocket upgrade failed", "remoteAddr", r.RemoteAddr, "scheme", r.URL.Scheme, "host", r.Host, "error", err)
		return
	}
	slog.Info("pairing websocket connected", "remoteAddr", r.RemoteAddr, "host", r.Host)

	var acceptedPairingID string
	var acceptedManualCode string
	var acceptedDevice struct {
		DeviceID    string `json:"deviceId"`
		DisplayName string `json:"displayName"`
		Platform    string `json:"platform"`
	}
	ignoredProbeCount := 0

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			slog.Info("pairing websocket read ended", "remoteAddr", r.RemoteAddr, "error", err)
			return
		}

		if messageType == websocket.TextMessage && string(message) == "ping" && ignoredProbeCount < 2 {
			ignoredProbeCount++
			slog.Debug("pairing websocket ignored legacy ping probe", "remoteAddr", r.RemoteAddr, "ignoredCount", ignoredProbeCount)
			continue
		}

		var claim struct {
			Type       string `json:"type"`
			PairingID  string `json:"pairingId"`
			ManualCode string `json:"manualCode"`
			Device     struct {
				DeviceID    string `json:"deviceId"`
				DisplayName string `json:"displayName"`
				Platform    string `json:"platform"`
			} `json:"device"`
		}

		if err := json.Unmarshal(message, &claim); err != nil {
			slog.Warn("pairing websocket invalid message", "remoteAddr", r.RemoteAddr, "error", err)
			sendPairingResult(conn, false, "invalid message format")
			return
		}

		if claim.Type != "pairing_claim" {
			slog.Warn("pairing websocket unexpected message type", "remoteAddr", r.RemoteAddr, "type", claim.Type)
			sendPairingResult(conn, false, "expected pairing_claim message")
			return
		}

		acceptedPairingID = claim.PairingID
		acceptedManualCode = claim.ManualCode
		acceptedDevice = claim.Device
		slog.Debug("pairing websocket accepted claim", "pairingId", acceptedPairingID, "hasManualCode", acceptedManualCode != "", "deviceId", acceptedDevice.DeviceID)
		break
	}

	// 支持 pairingId 或 manualCode 查找
	var session *PairingSession
	if acceptedPairingID != "" {
		session, err = globalPairingStore.Get(acceptedPairingID)
	} else if acceptedManualCode != "" {
		session, err = globalPairingStore.GetByManualCode(acceptedManualCode)
	} else {
		sendPairingResult(conn, false, "pairingId 或 manualCode 必须提供其一")
		return
	}
	if err != nil {
		slog.Warn("pairing session lookup failed", "remoteAddr", r.RemoteAddr, "pairingId", acceptedPairingID, "hasManualCode", acceptedManualCode != "", "error", err)
		sendPairingResult(conn, false, "pairing session lookup failed")
		return
	}
	if session == nil {
		slog.Warn("pairing session not found", "remoteAddr", r.RemoteAddr, "pairingId", acceptedPairingID, "hasManualCode", acceptedManualCode != "")
		sendPairingResult(conn, false, "pairing session not found")
		return
	}

	err = session.Claim(acceptedDevice.DeviceID, acceptedDevice.DisplayName, acceptedDevice.Platform)
	if err != nil {
		slog.Warn("pairing session claim failed", "remoteAddr", r.RemoteAddr, "pairingId", session.ID, "deviceId", acceptedDevice.DeviceID, "error", err)
		sendPairingResult(conn, false, err.Error())
		return
	}

	slog.Info("pairing websocket claimed", "remoteAddr", r.RemoteAddr, "pairingId", session.ID, "deviceId", acceptedDevice.DeviceID)
	// 先注册 pending connection，再暴露 pairing_result 给客户端和 Mac 端轮询。
	// 否则 Mac 端可能在 state=claimed 后立即 approve，导致 pairing_complete
	// 推送早于 registry 注册而丢失。
	actualPairingID := session.ID
	pending := &PairingPendingConn{
		pairingID: actualPairingID,
		conn:      conn,
		done:      make(chan struct{}),
	}
	if !globalPairingRegistry.Register(actualPairingID, pending) {
		// 同一个 pairingID 已有另一个连接注册 pending（并行 claim 竞速场景）。
		// 这个连接是竞速中的失败方，直接关闭，不等待 pairing_complete。
		slog.Warn("pairing websocket: pending already registered, closing duplicate", "pairingId", actualPairingID)
		sendPairingResult(conn, true, "")
		return
	}
	defer globalPairingRegistry.Unregister(actualPairingID)
	pending.writeMu.Lock()
	sendPairingResult(conn, true, "")
	pending.writeMu.Unlock()

	// 读 goroutine：持续读取客户端消息（包括 ping），让 gorilla/websocket
	// 在读路径自动回复 pong。防止 FRP 等中间代理因空闲超时断开连接。
	// 不设 read deadline：iOS 端会发 ping 保活，无 ping 时连接断开由 OS TCP
	// keepalive 检测。readDone 仅用于通知主流程客户端已断开。
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				slog.Debug("pairing keepalive read ended", "pairingId", actualPairingID, "error", err)
				return
			}
		}
	}()

	select {
	case <-pending.done:
		slog.Info("pairing websocket completed", "remoteAddr", r.RemoteAddr, "pairingId", actualPairingID)
		// 优雅关闭：给 iOS 端 500ms 处理 pairing_complete 消息，
		// 避免 conn.Close() 的 RST 先于 pairing_complete 到达。
		time.Sleep(500 * time.Millisecond)
	case <-readDone:
		// 客户端连接断开（网络中断或客户端关闭）
		slog.Warn("pairing websocket read ended before approval", "remoteAddr", r.RemoteAddr, "pairingId", actualPairingID)
	case <-time.After(5 * time.Minute):
		slog.Warn("pairing websocket timed out waiting approval", "remoteAddr", r.RemoteAddr, "pairingId", actualPairingID)
	}

	// 优雅关闭 WebSocket：发送 close frame 后等待对端确认
	conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second))
	conn.Close()
}

func sendPairingResult(conn *websocket.Conn, ok bool, errMsg string) {
	result := map[string]interface{}{
		"type": "pairing_result",
		"ok":   ok,
	}
	if !ok && errMsg != "" {
		result["error"] = map[string]string{
			"code":    "claim_failed",
			"message": errMsg,
		}
	}
	msg, _ := json.Marshal(result)
	conn.WriteMessage(websocket.TextMessage, msg)
}

// NotifyPairingComplete is called after Mac approves.
func NotifyPairingComplete(pairingID string, bridgeInfo PairingCompleteBridge) (string, string, error) {
	session, err := globalPairingStore.Get(pairingID)
	if err != nil {
		return "", "", err
	}

	plainToken, _, err := GenerateDeviceToken()
	if err != nil {
		return "", "", err
	}

	deviceID := session.ClaimingDeviceID
	if deviceID == "" {
		b := make([]byte, 4)
		rand.Read(b)
		deviceID = fmt.Sprintf("dev-%x", b)
	}

	_, _ = globalDeviceStore.ReplaceDevice(TrustedDeviceRecord{
		DeviceID:    deviceID,
		DisplayName: session.ClaimingDeviceName,
		Platform:    session.ClaimingPlatform,
		TokenHash:   HashToken(plainToken),
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	})

	session.Complete()

	push := PairingCompletePush{
		Type: "pairing_complete",
		Device: PairingCompleteDevice{
			DeviceID: deviceID,
			Token:    plainToken,
		},
		Bridge: bridgeInfo,
	}

	globalPairingRegistry.NotifyComplete(pairingID, push)

	return deviceID, plainToken, nil
}
