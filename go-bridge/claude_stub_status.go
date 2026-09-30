package gobridge

// claude_stub_status.go（badges 验收轮，抄官方作业）：Claude CLI 自带的
// ~/.claude/sessions/<pid>.json 会话注册表含官方 status 字段（busy|idle，
// statusUpdatedAt 随每次转移更新——即 Claude 官方桌面端的会话状态信号源）。
// 本轮询器以 1s 档读取全部 stub（数量＝活进程级，个位数），在状态转移时经
// 控制面入口发布 session_state_changed——外部（未在 iOS 打开）claude session
// 的列表徽标由此获得秒级鲜度（此前只能等列表刷新档，owner 实测 ~10s）。
//
// 死进程残桩（crash 遗留，官方语义「下次 register 剪枝」）：PID 已死且上次
// 观测为 busy → 发布 idle 一次并停止跟踪该 stub；残桩文件本身不动（属官方
// 注册表，下次 register 由 CLI 剪枝）。

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const claudeStubStatusInterval = 1 * time.Second

type claudeStubStatus struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Status    string `json:"status"` // busy | idle（官方词表；其他值不映射）
}

type claudeStubStatusPoller struct {
	h          *Handlers
	backendID  string
	sessionsDir string
	// lastPublished：sessionId → 上次已发布的 state（""＝未发布过）。nil 起步，
	// 首轮不发布（避免启动风暴——列表档会补齐初值）。
	lastPublished map[string]string
	stop          chan struct{}
}

func startClaudeStubStatusPoller(h *Handlers, backendID string) *claudeStubStatusPoller {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	p := &claudeStubStatusPoller{
		h:             h,
		backendID:     backendID,
		sessionsDir:   filepath.Join(home, ".claude", "sessions"),
		lastPublished: nil,
		stop:          make(chan struct{}),
	}
	go p.loop()
	return p
}

func (p *claudeStubStatusPoller) stopPoller() {
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
}

func (p *claudeStubStatusPoller) loop() {
	ticker := time.NewTicker(claudeStubStatusInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.pollOnce()
		}
	}
}

func (p *claudeStubStatusPoller) pollOnce() {
	entries, err := os.ReadDir(p.sessionsDir)
	if err != nil {
		return // 目录不存在（无 claude CLI 活动）＝静默空集
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		stub, ok := p.readStub(filepath.Join(p.sessionsDir, entry.Name()))
		if !ok || stub.SessionID == "" || stub.PID <= 0 {
			continue
		}
		seen[stub.SessionID] = true
		alive := pidAlive(stub.PID)
		state := ""
		switch {
		case !alive:
			// 死进程：官方语义为「会话已结束」——若此前发布过 running 则收口 idle。
			state = "idle"
		case stub.Status == "busy":
			state = "running"
		case stub.Status == "idle":
			state = "idle"
		default:
			continue // 未知官方 status 词：不映射（不知道就不亮）
		}
		if !alive && p.lastPublished[stub.SessionID] != "running" {
			continue // 死进程且本就没在转：无需收口
		}
		p.publishIfChanged(stub.SessionID, state)
	}
	// 消失的 stub（正常退出后 register 剪枝/卸载）：上次在转则收口 idle。
	for sid, last := range p.lastPublished {
		if last == "running" && !seen[sid] {
			p.publishIfChanged(sid, "idle")
		}
	}
}

func (p *claudeStubStatusPoller) publishIfChanged(sessionID, state string) {
	if p.lastPublished == nil {
		// 首轮：登记初值不发布（启动风暴防护；列表档补齐）。
		p.lastPublished = map[string]string{sessionID: state}
		return
	}
	if p.lastPublished[sessionID] == state {
		return
	}
	p.lastPublished[sessionID] = state
	slog.Debug("go-bridge: claude stub status transition",
		"sessionPrefix", projectionSessionLogPrefix(sessionID), "state", state)
	p.h.eventPublisher.PublishSessionStateControlPlane(p.backendID, sessionID, state)
}

func (p *claudeStubStatusPoller) readStub(path string) (claudeStubStatus, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return claudeStubStatus{}, false
	}
	var stub claudeStubStatus
	if err := json.Unmarshal(data, &stub); err != nil {
		return claudeStubStatus{}, false
	}
	return stub, true
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
