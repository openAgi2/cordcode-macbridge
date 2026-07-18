package gobridge

import (
	"log/slog"
	"sync"
)

// DeviceConnRegistry tracks active connections by device ID.
// 用于 revoke 时主动下发 device_revoked 事件并断开连接。
//
// 同时覆盖 direct（*Conn 经 directConnAdapter）和 relay（*RelayDeviceConn）两条路径：
// 场景4 修复前 relay 连接未注册到此 registry，导致 Mac 撤销授权时 relay 连接
// 收不到 device_revoked 事件、不断开，iOS 继续可用。现统一注册。
type DeviceConnRegistry struct {
	mu             sync.Mutex
	conns          map[string][]Connection // deviceID → connections（direct 或 relay）
	eventPublisher *EventPublisher
}

func (r *DeviceConnRegistry) SetEventPublisher(publisher *EventPublisher) {
	r.mu.Lock()
	r.eventPublisher = publisher
	r.mu.Unlock()
}

var globalDeviceConnRegistry = &DeviceConnRegistry{
	conns: make(map[string][]Connection),
}

func (r *DeviceConnRegistry) Register(deviceID string, conn Connection) {
	if deviceID == "" || conn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns[deviceID] = append(r.conns[deviceID], conn)
}

func (r *DeviceConnRegistry) Unregister(deviceID string, conn Connection) {
	if deviceID == "" || conn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	conns := r.conns[deviceID]
	for i, c := range conns {
		if c == conn {
			r.conns[deviceID] = append(conns[:i], conns[i+1:]...)
			break
		}
	}
	if len(r.conns[deviceID]) == 0 {
		delete(r.conns, deviceID)
	}
}

// DisconnectDevice 关闭指定设备的所有连接，发送 device_revoked 事件后主动 Close。
// 修复场景4：补 Close 确保撤销即时生效（原 direct 仅 SendJSON 不 Close，依赖 iOS 侧断开；
// 现统一发事件 + Close，relay 路径也能即时断开）。
func (r *DeviceConnRegistry) DisconnectDevice(deviceID string) {
	r.mu.Lock()
	conns := r.conns[deviceID]
	publisher := r.eventPublisher
	delete(r.conns, deviceID)
	r.mu.Unlock()

	for _, conn := range conns {
		slog.Info("go-bridge: marking device revoked", "deviceId", deviceID, "remote", conn.RemoteAddr())
		// 先下发 device_revoked 事件（iOS 侧据此删 bridge + 清凭证），
		// 再 Close 确保即使 iOS 未及时处理事件，连接也被强制断开。
		if publisher != nil {
			publisher.PublishLogical(LogicalEvent{
				Event:       "device_revoked",
				Message:     "设备授权已取消，请重新授权",
				Targets:     []Connection{conn},
				WaitTargets: []Connection{conn},
			})
		}
		if err := conn.Close(); err != nil {
			slog.Debug("go-bridge: close revoked device conn failed", "deviceId", deviceID, "error", err)
		}
	}
}
