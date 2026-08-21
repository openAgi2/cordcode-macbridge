package codexweb

// diagnostics.go —— 版本/transport/能力/失败原文诊断（§5.2/§6.2/§11.2）。
//
// InstanceStatus 是 go-bridge descriptor 的只读镜像（instanceStatusProber 约定）：
// 镜像最近一次 Probe 结果，不主动探测/拉起任何进程——探测归属 lifecycle
// （首次目录/历史请求触发）。§6.3 的来源区分（external-daemon-reused 与
// cordcode-started-daemon）保留在 ProbeSnapshot.Source，供 ownership 排查。

import (
	"fmt"
	"strings"
)

// InstanceStatus 返回 (available, detail)。detail 携带官方失败原文或来源/版本。
func (a *Agent) InstanceStatus() (bool, string) {
	a.mu.Lock()
	snap := a.lastStatus
	a.mu.Unlock()
	if snap == nil {
		return false, "codex-web: 尚未建立官方服务连接（首次目录/历史请求时探测）"
	}
	if !snap.Available {
		return false, snap.Detail
	}
	return true, fmt.Sprintf("codex app-server %s（%s）", strings.TrimSpace(snap.CLIVersion), snap.Source)
}
