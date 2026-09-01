package gobridge

// Connection 是 direct 和 relay 连接的最小业务接口。
// handlers.go 通过此接口发送消息，不感知底层是 WebSocket 直连还是 relay 加密通道。
//
// 方案 §10.1：
//
//	"定义 direct/relay 共用的最小 Connection 接口并适配现有 handlers。"
type Connection interface {
	// SendJSON 发送一条 JSON 消息。实现必须保证并发安全。
	SendJSON(v any)

	// SendResult 发送带 requestId 的 result 回复。
	SendResult(requestID string, data interface{}, err *WireError)

	// AuthedDevice 返回已认证的设备记录；未认证返回 nil。
	AuthedDevice() *TrustedDeviceRecord

	// RemoteAddr 返回远端地址描述，用于日志。
	RemoteAddr() string

	// Close 关闭连接。
	Close() error
}

// directConnAdapter 将现有 *Conn 包装为 Connection 接口。
// 业务逻辑不应再直接依赖 *Conn，应统一使用 Connection。
type directConnAdapter struct {
	inner *Conn
}

var _ Connection = (*directConnAdapter)(nil)

func adaptDirectConn(c *Conn) Connection {
	return &directConnAdapter{inner: c}
}

func (d *directConnAdapter) SendJSON(v any) {
	d.inner.SendJSON(v)
}

// SendJSONReport forwards to the inner *Conn so K4 write_post can observe write errors.
// Without this method the sink type-assert fails and falls back to plain SendJSON
// (which swallows closed-conn / WriteJSON failures).
func (d *directConnAdapter) SendJSONReport(v any) error {
	return d.inner.SendJSONReport(v)
}

func (d *directConnAdapter) SendResult(requestID string, data interface{}, err *WireError) {
	d.inner.SendResult(requestID, data, err)
}

func (d *directConnAdapter) AuthedDevice() *TrustedDeviceRecord {
	return d.inner.authedDevice
}

func (d *directConnAdapter) RemoteAddr() string {
	return d.inner.remote
}

// IsRevoked 检查底层直连是否已被撤销。
func (d *directConnAdapter) IsRevoked() bool {
	return d.inner.revoked
}

// isClosed forwards the stale-socket check to the wrapped direct connection.
// Broadcaster/Publisher use this optional seam for both direct and relay paths;
// without forwarding it, a closed direct socket can remain a candidate target
// until its cleanup callback happens to run.
func (d *directConnAdapter) isClosed() bool {
	return d == nil || d.inner == nil || d.inner.isClosed()
}

func (d *directConnAdapter) Close() error {
	return d.inner.Close()
}
