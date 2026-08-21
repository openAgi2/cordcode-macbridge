package codexweb

// metrics.go —— §13.2 帧级指标采集（send→started、首 delta、delta 数/字符、
// 相邻间隔、完成延迟；app-server/bridge 进程计数由运维面观察，不在 adapter 内造）。
//
// 纪律（§2.5-4）：指标只记录 adapter 实收的官方帧事实，区分 provider 单帧与
// adapter 行为；不据此修改事件流。

import (
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TurnMetrics 是一个官方 turn 的帧级时间线。
type TurnMetrics struct {
	ThreadID string
	TurnID   string

	SendAt         time.Time // 本端 turn/start 发出（Send 路径记录）
	StartedAt      time.Time // 官方 turn/started 到达
	FirstDeltaAt   time.Time // 首个正文 delta 到达
	CompletedAt    time.Time // 官方终态到达
	DeltaCount     int
	DeltaChars     int
	MaxDeltaChars  int
	MaxInterDelta  time.Duration // 相邻 delta 最大间隔
	lastDeltaAt    time.Time
}

// SendToStarted / SendToFirstDelta / TurnLatency 返回 §13.2 关键延迟。
func (m *TurnMetrics) SendToStarted() time.Duration {
	if m.SendAt.IsZero() || m.StartedAt.IsZero() {
		return 0
	}
	return m.StartedAt.Sub(m.SendAt)
}

func (m *TurnMetrics) SendToFirstDelta() time.Duration {
	if m.SendAt.IsZero() || m.FirstDeltaAt.IsZero() {
		return 0
	}
	return m.FirstDeltaAt.Sub(m.SendAt)
}

func (m *TurnMetrics) TurnLatency() time.Duration {
	if m.SendAt.IsZero() || m.CompletedAt.IsZero() {
		return 0
	}
	return m.CompletedAt.Sub(m.SendAt)
}

func metricsKey(threadID, turnID string) string { return threadID + "/" + turnID }

// noteSend 记录一次 turn/start 发出时刻（Send 路径）。
func (a *Agent) noteSend(threadID string, at time.Time) {
	a.metricsMu.Lock()
	defer a.metricsMu.Unlock()
	a.sendAt[threadID] = at
}

// recordMetrics 由中央泵对每条解码事件调用（事件时间 = 到达时间）。
func (a *Agent) recordMetrics(ev core.Event) {
	if ev.TurnID == "" {
		return
	}
	a.metricsMu.Lock()
	defer a.metricsMu.Unlock()
	key := metricsKey(ev.SessionID, ev.TurnID)
	m := a.turnMetrics[key]
	if m == nil {
		m = &TurnMetrics{ThreadID: ev.SessionID, TurnID: ev.TurnID}
		a.turnMetrics[key] = m
	}
	now := time.Now()
	switch ev.Type {
	case core.EventTurnStarted:
		m.StartedAt = now
		if send := a.sendAt[ev.SessionID]; !send.IsZero() && m.SendAt.IsZero() {
			m.SendAt = send
		}
	case core.EventText:
		m.DeltaCount++
		m.DeltaChars += len(ev.Content)
		if len(ev.Content) > m.MaxDeltaChars {
			m.MaxDeltaChars = len(ev.Content)
		}
		if m.FirstDeltaAt.IsZero() {
			m.FirstDeltaAt = now
		}
		if !m.lastDeltaAt.IsZero() {
			if gap := now.Sub(m.lastDeltaAt); gap > m.MaxInterDelta {
				m.MaxInterDelta = gap
			}
		}
		m.lastDeltaAt = now
	case core.EventResult, core.EventError:
		if ev.Done {
			m.CompletedAt = now
			if send := a.sendAt[ev.SessionID]; !send.IsZero() && m.SendAt.IsZero() {
				m.SendAt = send
			}
		}
	}
}

// MetricsSnapshot 返回当前全部 turn 指标（A/B 对照脚本消费）。
func (a *Agent) MetricsSnapshot() []TurnMetrics {
	a.metricsMu.Lock()
	defer a.metricsMu.Unlock()
	out := make([]TurnMetrics, 0, len(a.turnMetrics))
	for _, m := range a.turnMetrics {
		out = append(out, *m)
	}
	return out
}
