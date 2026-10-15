package gobridge

import (
	"log/slog"
	"sort"
	"sync"
)

type DeviceConnectionTransport string

const (
	DeviceConnectionTransportDirect  DeviceConnectionTransport = "direct"
	DeviceConnectionTransportRelay   DeviceConnectionTransport = "relay"
	DeviceConnectionTransportUnknown DeviceConnectionTransport = "unknown"
)

type DeviceConnectionMetadata struct {
	Kind        DeviceConnectionTransport
	DirectClass string
}

type DevicePresenceTransport struct {
	Kind        DeviceConnectionTransport `json:"kind"`
	DirectClass string                    `json:"directClass,omitempty"`
}

type DevicePresence struct {
	DeviceID   string                    `json:"deviceId"`
	Transports []DevicePresenceTransport `json:"transports"`
}

type deviceConnEntry struct {
	conn     Connection
	metadata DeviceConnectionMetadata
}

type deviceConnectionMetadataProvider interface {
	ConnectionTransportMetadata() DeviceConnectionMetadata
}

// DeviceConnRegistry tracks active connections by device ID.
// 用于 revoke 时主动下发 device_revoked 事件并断开连接。
//
// 同时覆盖 direct（*Conn 经 directConnAdapter）和 relay（*RelayDeviceConn）两条路径：
// 场景4 修复前 relay 连接未注册到此 registry，导致 Mac 撤销授权时 relay 连接
// 收不到 device_revoked 事件、不断开，iOS 继续可用。现统一注册。
type DeviceConnRegistry struct {
	mu             sync.Mutex
	conns          map[string][]deviceConnEntry // deviceID → connections（direct 或 relay）
	eventPublisher *EventPublisher
}

func (r *DeviceConnRegistry) SetEventPublisher(publisher *EventPublisher) {
	r.mu.Lock()
	r.eventPublisher = publisher
	r.mu.Unlock()
}

var globalDeviceConnRegistry = &DeviceConnRegistry{
	conns: make(map[string][]deviceConnEntry),
}

func (r *DeviceConnRegistry) Register(deviceID string, conn Connection) {
	if deviceID == "" || conn == nil {
		return
	}
	metadata := connectionMetadata(conn)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns[deviceID] = append(r.conns[deviceID], deviceConnEntry{conn: conn, metadata: metadata})
}

func (r *DeviceConnRegistry) Unregister(deviceID string, conn Connection) {
	if deviceID == "" || conn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := r.conns[deviceID]
	for i, entry := range entries {
		if entry.conn == conn {
			r.conns[deviceID] = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	if len(r.conns[deviceID]) == 0 {
		delete(r.conns, deviceID)
	}
}

// Connections returns a snapshot of live connections for a device (direct and/or relay).
// Used to rebind broadcaster session subscriptions when live targets go to zero while
// the device still has an open transport (path-switch / thrash windows).
func (r *DeviceConnRegistry) Connections(deviceID string) []Connection {
	if deviceID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	src := r.conns[deviceID]
	if len(src) == 0 {
		return nil
	}
	out := make([]Connection, len(src))
	for i, entry := range src {
		out[i] = entry.conn
	}
	return out
}

// AllDeviceIDs returns device IDs that currently have at least one registered connection.
func (r *DeviceConnRegistry) AllDeviceIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.conns))
	for id := range r.conns {
		out = append(out, id)
	}
	return out
}

// AllConnections returns a snapshot of every live direct and relay connection.
// Used to push control-plane frames (e.g. advertised LAN URL refresh) without
// going through EventPublisher.
func (r *DeviceConnRegistry) AllConnections() []Connection {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int
	for _, entries := range r.conns {
		n += len(entries)
	}
	if n == 0 {
		return nil
	}
	out := make([]Connection, 0, n)
	for _, entries := range r.conns {
		for _, entry := range entries {
			out = append(out, entry.conn)
		}
	}
	return out
}

// PresenceSnapshot returns a de-duplicated transport projection from the same live
// registry used for revoke. It does not infer liveness from persisted lastSeenAt.
func (r *DeviceConnRegistry) PresenceSnapshot() []DevicePresence {
	if r == nil {
		return []DevicePresence{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.conns))
	for deviceID := range r.conns {
		ids = append(ids, deviceID)
	}
	sort.Strings(ids)
	out := make([]DevicePresence, 0, len(ids))
	for _, deviceID := range ids {
		seen := make(map[DevicePresenceTransport]struct{})
		transports := make([]DevicePresenceTransport, 0)
		for _, entry := range r.conns[deviceID] {
			transport := DevicePresenceTransport{
				Kind: entry.metadata.Kind, DirectClass: entry.metadata.DirectClass,
			}
			if transport.Kind == DeviceConnectionTransportUnknown && transport.DirectClass == "" {
				transport.Kind = DeviceConnectionTransportUnknown
				transport.DirectClass = "unknown"
			}
			if _, duplicate := seen[transport]; duplicate {
				continue
			}
			seen[transport] = struct{}{}
			transports = append(transports, transport)
		}
		sort.Slice(transports, func(i, j int) bool {
			if transports[i].Kind != transports[j].Kind {
				return transports[i].Kind < transports[j].Kind
			}
			return transports[i].DirectClass < transports[j].DirectClass
		})
		out = append(out, DevicePresence{DeviceID: deviceID, Transports: transports})
	}
	return out
}

func connectionMetadata(conn Connection) DeviceConnectionMetadata {
	metadata := DeviceConnectionMetadata{
		Kind: DeviceConnectionTransportUnknown, DirectClass: "unknown",
	}
	if provider, ok := conn.(deviceConnectionMetadataProvider); ok {
		metadata = provider.ConnectionTransportMetadata()
	}
	switch metadata.Kind {
	case DeviceConnectionTransportDirect:
		if metadata.DirectClass == "" {
			metadata.DirectClass = "unknown"
		}
	case DeviceConnectionTransportRelay:
		metadata.DirectClass = ""
	default:
		metadata.Kind = DeviceConnectionTransportUnknown
		if metadata.DirectClass == "" {
			metadata.DirectClass = "unknown"
		}
	}
	return metadata
}

// DisconnectDevice 关闭指定设备的所有连接，发送 device_revoked 事件后主动 Close。
// 修复场景4：补 Close 确保撤销即时生效（原 direct 仅 SendJSON 不 Close，依赖 iOS 侧断开；
// 现统一发事件 + Close，relay 路径也能即时断开）。
func (r *DeviceConnRegistry) DisconnectDevice(deviceID string) {
	r.mu.Lock()
	entries := r.conns[deviceID]
	publisher := r.eventPublisher
	delete(r.conns, deviceID)
	r.mu.Unlock()

	for _, entry := range entries {
		conn := entry.conn
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
