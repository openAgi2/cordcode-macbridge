package codexweb

// transport.go —— 字节传输与重连（设计 §5.2/§6.1）。
//
// 协议事实（Phase 0 实测 + unix_socket.rs accept_async）：
//   - app-server stdio transport：newline-delimited JSON（每行一个 JSON-RPC 消息）；
//   - daemon control socket：WebSocket over Unix socket（WS 升级；每个 JSON-RPC 消息一个
//     WS text 帧）；裸 newline JSON 连接会被立即关闭；
//   - 官方 `codex app-server proxy --sock` 是纯字节中继，客户端仍需自行讲 WS——因此本包
//     不经 proxy，直接实现 WS-over-UDS。
//
// 安全（§11.1）：托管 WS 只绑 127.0.0.1；不把 control socket 暴露进 Relay。

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/gorilla/websocket"
)

// Transport 是与官方 app-server 的传输抽象（rpc.go 在其上做 JSON-RPC 相关性）。
type Transport interface {
	// Send 写一个 JSON-RPC 消息帧；Recv 读下一帧；Close 关闭并使后续读写失败。
	Send(payload []byte) error
	Recv() ([]byte, error)
	Close() error
}

const wsHandshakeTimeout = 10 * time.Second

// wsTransport 承载 WS-over-UDS 与 loopback WS（每 JSON-RPC 消息一个 text 帧）。
type wsTransport struct {
	conn *websocket.Conn
}

// DialWSUnix 经 Unix domain socket 完成 WebSocket 升级并连接官方 daemon control socket。
func DialWSUnix(ctx context.Context, socketPath string) (Transport, error) {
	d := websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var nd net.Dialer
			return nd.DialContext(ctx, "unix", socketPath)
		},
		HandshakeTimeout: wsHandshakeTimeout,
	}
	// Host 头仅为握手合法性；官方 acceptor 不校验 Host（Phase 0 gate_terminal.py 同法直连成功）。
	conn, _, err := d.DialContext(ctx, "ws://codex-daemon/", nil)
	if err != nil {
		return nil, fmt.Errorf("ws-over-uds dial %s: %w", socketPath, err)
	}
	return &wsTransport{conn: conn}, nil
}

// DialWSTCP 连接显式/托管的 loopback WebSocket service。
func DialWSTCP(ctx context.Context, url string) (Transport, error) {
	d := websocket.Dialer{HandshakeTimeout: wsHandshakeTimeout}
	conn, _, err := d.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("ws dial %s: %w", url, err)
	}
	return &wsTransport{conn: conn}, nil
}

func (t *wsTransport) Send(payload []byte) error {
	return t.conn.WriteMessage(websocket.TextMessage, payload)
}

func (t *wsTransport) Recv() ([]byte, error) {
	_, data, err := t.conn.ReadMessage()
	return data, err
}

func (t *wsTransport) Close() error { return t.conn.Close() }

// stdioTransport 是 `codex app-server` 子进程的 newline JSON 传输（托管/内嵌形态使用）。
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	closed chan struct{}
}

// StartStdio 启动官方 app-server stdio 子进程（CODEX_HOME 隔离由调用方注入 env）。
func StartStdio(bin, codexHome, workDir string, extraEnv []string) (Transport, error) {
	cmd := exec.Command(bin, "app-server")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	cmd.Env = append(cmd.Env, extraEnv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s app-server: %w", bin, err)
	}
	return &stdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		closed: make(chan struct{}),
	}, nil
}

func (t *stdioTransport) Send(payload []byte) error {
	select {
	case <-t.closed:
		return fmt.Errorf("codexweb: stdio transport closed")
	default:
	}
	if _, err := t.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("stdio send: %w", err)
	}
	return nil
}

func (t *stdioTransport) Recv() ([]byte, error) {
	line, err := t.stdout.ReadString('\n')
	if err != nil {
		return nil, err
	}
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return []byte(line), nil
}

func (t *stdioTransport) Close() error {
	select {
	case <-t.closed:
		return nil
	default:
		close(t.closed)
	}
	_ = t.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- t.cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		_ = t.cmd.Process.Kill()
		<-done
		return fmt.Errorf("codexweb: app-server stdio shutdown exceeded 5s, killed")
	}
}
