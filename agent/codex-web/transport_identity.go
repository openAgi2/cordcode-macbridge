package codexweb

// transport_identity.go —— S0：codex-web 后端 main/observer 传输身份的只读
// provider（implementation plan v2 §2.1）。只从现有连接状态组装快照；不建立、
// 不恢复、不打断任何连接。错误码语义：角色未建立/已关闭 → none（明确未附着）；
// 探测未完成 → unknown（证据不足）；provider 本身无失败路径（快照恒成功）。

import (
	"context"
	"strconv"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TransportIdentitySnapshot 实现 core.CodexWebTransportIdentityProvider。
func (a *Agent) TransportIdentitySnapshot(_ context.Context) (core.CodexWebTransportIdentity, error) {
	return a.snapshotTransportIdentity(), nil
}

func (a *Agent) snapshotTransportIdentity() core.CodexWebTransportIdentity {
	id := core.CodexWebTransportIdentity{SampledAtMs: time.Now().UnixNano() / int64(time.Millisecond)}

	a.mu.Lock()
	ep := a.endpoint
	stopped := a.stopped
	probing := a.probing
	probeDone := a.probeDone
	var main *Client
	if ep != nil {
		// endpoint.Client is the established main transport. pumpClient only
		// records whether a session listener currently owns its notification
		// pump; idle must not be reported as detached.
		main = ep.Client()
	}
	a.mu.Unlock()

	id.Endpoint = endpointOf(ep)

	if main != nil {
		id.Main = a.roleState(stopped, main)
		if id.Main.Attached {
			id.Main.PeerKey = peerKey(id.Endpoint, id.Main.Epoch)
		}
	} else if probing || probeDone == nil {
		id.Main = core.CodexWebTransportRoleState{ErrorCode: "unknown"}
	} else {
		id.Main = core.CodexWebTransportRoleState{ErrorCode: "none"}
	}

	a.obsMu.Lock()
	obs := a.obsClient
	a.obsMu.Unlock()
	if obs != nil {
		id.Observer = a.roleState(stopped, obs)
		if id.Observer.Attached {
			id.Observer.PeerKey = peerKey(id.Endpoint, id.Observer.Epoch)
		}
	} else if probing || probeDone == nil {
		id.Observer = core.CodexWebTransportRoleState{ErrorCode: "unknown"}
	} else {
		id.Observer = core.CodexWebTransportRoleState{ErrorCode: "none"}
	}

	if id.Main.Epoch > id.Observer.Epoch {
		id.Epoch = id.Main.Epoch
	} else {
		id.Epoch = id.Observer.Epoch
	}
	return id
}

// roleState 由现存 Client 组装角色态：已关闭或 backend 已停止 = 明确未附着。
func (a *Agent) roleState(stopped bool, c *Client) core.CodexWebTransportRoleState {
	st := core.CodexWebTransportRoleState{Epoch: int64(c.Epoch()), ErrorCode: "none"}
	if !c.IsClosed() && !stopped {
		st.Attached = true
	}
	return st
}

func endpointOf(ep *ServiceEndpoint) string {
	if ep == nil {
		return ""
	}
	if ep.UnixSocket != "" {
		return ep.UnixSocket
	}
	return ep.TCPEndpoint
}

// peerKey 是与传输/FD 证据关联的键：endpoint#epoch（§2.1 PeerKey 语义）。
func peerKey(endpoint string, epoch int64) string {
	if endpoint == "" {
		return ""
	}
	return endpoint + "#" + strconv.FormatInt(epoch, 10)
}
