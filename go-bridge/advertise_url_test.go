package gobridge

import (
	"net"
	"testing"
)

func TestBuildBridgeLocalURL_IncludesBridgePath(t *testing.T) {
	got := BuildBridgeLocalURL("192.168.1.25", 8777)
	want := "ws://192.168.1.25:8777/bridge"
	if got != want {
		t.Fatalf("BuildBridgeLocalURL() = %q, want %q", got, want)
	}
}

func TestSelectPreferredAdvertiseIP_PrefersPrivateIPv4(t *testing.T) {
	selected := selectPreferredAdvertiseIP([]net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("8.8.8.8"),
		net.ParseIP("192.168.31.8"),
	})
	if selected == nil || selected.String() != "192.168.31.8" {
		t.Fatalf("selected = %v, want 192.168.31.8", selected)
	}
}

func TestSelectPreferredAdvertiseIP_FallsBackToGlobalIPv4(t *testing.T) {
	selected := selectPreferredAdvertiseIP([]net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("8.8.8.8"),
	})
	if selected == nil || selected.String() != "8.8.8.8" {
		t.Fatalf("selected = %v, want 8.8.8.8", selected)
	}
}

// ── LAN 候选过滤(Phase A 双路径配对)── 纯函数 isLanCandidateIPv4 / isLikelyVirtualInterface ──

func TestIsLanCandidateIPv4_AcceptsPrivateLAN(t *testing.T) {
	for _, ip := range []string{"192.168.1.25", "10.0.0.5", "172.16.0.1", "172.31.255.254"} {
		if !isLanCandidateIPv4(net.ParseIP(ip)) {
			t.Errorf("isLanCandidateIPv4(%s) = false, want true (private LAN)", ip)
		}
	}
}

func TestIsLanCandidateIPv4_RejectsNonCandidates(t *testing.T) {
	cases := map[string]string{
		"loopback":           "127.0.0.1",
		"link-local":         "169.254.1.1",
		"tailscale CGNAT lo": "100.64.0.1",
		"tailscale CGNAT hi": "100.127.255.254",
		"public":             "8.8.8.8",
		"non-private 100.x":  "100.128.0.1",
	}
	for name, ip := range cases {
		if isLanCandidateIPv4(net.ParseIP(ip)) {
			t.Errorf("isLanCandidateIPv4(%s=%s) = true, want false", name, ip)
		}
	}
}

func TestIsLanCandidateIPv4_RejectsIPv6(t *testing.T) {
	for _, ip := range []string{"::1", "fd00::1", "fe80::1"} {
		if isLanCandidateIPv4(net.ParseIP(ip)) {
			t.Errorf("isLanCandidateIPv4(%s) = true, want false (IPv6 not a LAN candidate)", ip)
		}
	}
}

func TestIsLikelyVirtualInterface(t *testing.T) {
	for _, n := range []string{"docker0", "docker1", "br-abcdef", "veth0", "virbr0"} {
		if !isLikelyVirtualInterface(n) {
			t.Errorf("isLikelyVirtualInterface(%q) = false, want true (container/virtual)", n)
		}
	}
	// macOS 物理/共享/VM 接口故意保留:race 自然忽略不可达候选,但不可误杀真实承载接口。
	for _, n := range []string{"en0", "en1", "bridge100", "vmnet8", "utun0", "vnic0"} {
		if isLikelyVirtualInterface(n) {
			t.Errorf("isLikelyVirtualInterface(%q) = true, want false (macOS interface must be kept)", n)
		}
	}
}

func TestBuildBridgeLocalURLs_ExplicitHostOnly(t *testing.T) {
	t.Setenv("GO_BRIDGE_ADVERTISE_HOST", "192.168.1.99")
	got := BuildBridgeLocalURLs(8777)
	want := []string{"ws://192.168.1.99:8777/bridge"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("BuildBridgeLocalURLs(explicit host) = %v, want %v", got, want)
	}
}
