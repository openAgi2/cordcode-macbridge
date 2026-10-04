package gobridge

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"
)

// advertisedURLRefreshInterval 是运行期重算 LAN 广播地址的周期。
// DHCP 换 IP 后最坏等待该间隔才会把新 ws:// 推给已连接的 iPhone。
var advertisedURLRefreshInterval = 10 * time.Second

// helloIdentity 是 hello / hello_ack 读取的 Bridge 身份快照。
type helloIdentity struct {
	bridgeID           string
	displayName        string
	runtimeVersion     string
	localURL           string
	remoteURL          string
	remoteURLs         []string
	localCandidateURLs []string
	connectionPolicy   ConnectionPolicy
}

func advertisedHostIsExplicit() bool {
	return strings.TrimSpace(os.Getenv("GO_BRIDGE_ADVERTISE_HOST")) != ""
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Server) helloIdentitySnapshot() helloIdentity {
	s.identityMu.RLock()
	defer s.identityMu.RUnlock()
	return helloIdentity{
		bridgeID:           s.bridgeID,
		displayName:        s.displayName,
		runtimeVersion:     s.runtimeVersion,
		localURL:           s.localURL,
		remoteURL:          s.remoteURL,
		remoteURLs:         append([]string(nil), s.remoteURLs...),
		localCandidateURLs: append([]string(nil), s.localCandidateURLs...),
		connectionPolicy:   s.connectionPolicy,
	}
}

// AdvertisedLocalURLs 返回当前对外广播的 primary LAN URL 与完整候选列表（主候选在前）。
func (s *Server) AdvertisedLocalURLs() (local string, locals []string) {
	s.identityMu.RLock()
	defer s.identityMu.RUnlock()
	return s.localURL, append([]string(nil), s.localCandidateURLs...)
}

// applyAdvertisedLocalURLs 在身份锁内写入新的 LAN 广播地址。未变化返回 false。
func (s *Server) applyAdvertisedLocalURLs(local string, locals []string) bool {
	locals = uniqueNonEmptyStrings(locals)
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	if s.localURL == local && stringSlicesEqual(s.localCandidateURLs, locals) {
		return false
	}
	s.localURL = local
	s.localCandidateURLs = locals
	return true
}

func buildBridgeCurrentURLsPayload(local string, locals []string) map[string]any {
	return map[string]any{
		"type": "bridge_current_urls",
		"currentURLs": HelloURLs{
			Local:  local,
			Locals: filterOutString(locals, local),
		},
	}
}

func (s *Server) broadcastCurrentURLs() {
	local, locals := s.AdvertisedLocalURLs()
	payload := buildBridgeCurrentURLsPayload(local, locals)
	conns := globalDeviceConnRegistry.AllConnections()
	for _, conn := range conns {
		if conn == nil {
			continue
		}
		conn.SendJSON(payload)
	}
	slog.Info("go-bridge: advertised LAN URLs pushed",
		"local", local,
		"locals", len(filterOutString(locals, local)),
		"targets", len(conns),
	)
}

// resolveAdvertisedLocalURLsFn 是 LAN 广播地址解析缝：生产读真实网卡；
// 测试注入确定性地址序列验证变化/不变分支。
var resolveAdvertisedLocalURLsFn = func(port int) (string, []string) {
	return BuildBridgeLocalURL(ResolveAdvertisedHost(), port), BuildBridgeLocalURLs(port)
}

// RefreshAdvertisedLocalURLs 按当前网卡重算 LAN 广播地址。
// 显式 GO_BRIDGE_ADVERTISE_HOST 保持 sticky，不覆盖。地址变化时更新 hello 身份并
// 向已连接设备（直连 + Relay）推送 top-level bridge_current_urls。
func (s *Server) RefreshAdvertisedLocalURLs(port int) bool {
	if advertisedHostIsExplicit() {
		return false
	}
	local, locals := resolveAdvertisedLocalURLsFn(port)
	prev, _ := s.AdvertisedLocalURLs()
	if !s.applyAdvertisedLocalURLs(local, locals) {
		return false
	}
	slog.Info("go-bridge: advertised LAN URLs refreshed", "from", prev, "to", local)
	s.broadcastCurrentURLs()
	return true
}

// runAdvertisedLocalURLRefresher 周期重算 LAN 广播地址，并把变化同步到 Management API
//（新配对 / remote status）与已连接客户端。ctx 取消后退出。
func runAdvertisedLocalURLRefresher(ctx context.Context, server *Server, mgmt *ManagementServer, port int) {
	if advertisedHostIsExplicit() {
		slog.Info("go-bridge: advertised host is explicit; skip LAN IP refresh")
		return
	}
	ticker := time.NewTicker(advertisedURLRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !server.RefreshAdvertisedLocalURLs(port) {
				continue
			}
			if mgmt != nil {
				local, locals := server.AdvertisedLocalURLs()
				mgmt.SetLocalURLs(local, locals)
			}
		}
	}
}
