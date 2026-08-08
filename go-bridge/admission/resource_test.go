package admission

import (
	"strings"
	"testing"
)

func validFRHConfig() FileReadHealthConfig {
	return FileReadHealthConfig{PoolSize: 8, MinHealthyFileSlots: 2, DegradeAt: 5, StuckAgeMillis: 30000}
}

func TestFileReadHealthConfig_Valid(t *testing.T) {
	if err := validFRHConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestFileReadHealthConfig_DegradeBeforeFullLoss(t *testing.T) {
	// poolSize=8, minHealthy=2 => healthySlots=6. degradeAt must be <= 6.
	c := validFRHConfig()
	c.DegradeAt = 7 // > 6：在全部 slot 损失前未报警
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "degradeAt") {
		t.Fatalf("expected degradeAt invariant violation, got %v", err)
	}
	// boundary: degradeAt == healthySlots (6) ok
	c.DegradeAt = 6
	if err := c.Validate(); err != nil {
		t.Errorf("degradeAt==healthySlots should be ok: %v", err)
	}
}

func TestFileReadHealthConfig_MinHealthyExceedsPool(t *testing.T) {
	c := validFRHConfig()
	c.MinHealthyFileSlots = 10 // > poolSize=8
	if err := c.Validate(); err == nil {
		t.Error("expected minHealthyFileSlots>poolSize rejection")
	}
}

// FileReadHealth 状态转换：healthy→degrading→degraded，每次转换 stateEpoch 恰增 1；幂等不增。
func TestFileReadHealth_TransitionsAndEpoch(t *testing.T) {
	h := NewFileReadHealthMachine()
	if h.Health() != HealthHealthy || h.StateEpoch() != 1 {
		t.Fatalf("initial: health=%v epoch=%d", h.Health(), h.StateEpoch())
	}
	h.MarkDegrading()
	if h.Health() != HealthDegrading || h.StateEpoch() != 2 {
		t.Errorf("after degrading: health=%v epoch=%d want degrading/2", h.Health(), h.StateEpoch())
	}
	h.MarkDegrading() // 幂等，不增
	if h.StateEpoch() != 2 {
		t.Errorf("idempotent degrading must not bump epoch; got %d", h.StateEpoch())
	}
	h.MarkDegraded()
	if h.Health() != HealthDegraded || h.StateEpoch() != 3 {
		t.Errorf("after degraded: health=%v epoch=%d want degraded/3", h.Health(), h.StateEpoch())
	}
	h.MarkDegraded() // 幂等
	if h.StateEpoch() != 3 {
		t.Errorf("idempotent degraded must not bump epoch; got %d", h.StateEpoch())
	}
}

// FileReadHealth 与 RuntimeAdmission 正交：degraded+accepting 是合法组合。
func TestFileReadHealth_OrthogonalToAdmission(t *testing.T) {
	clock := &fakeClock{}
	m := newAcceptingMachine(clock) // admission=accepting
	h := NewFileReadHealthMachine()
	h.MarkDegraded() // health=degraded
	// admission 仍 accepting，health degraded —— 正交，组合合法
	if m.State() != StateAccepting {
		t.Errorf("admission must stay accepting independent of health")
	}
	if h.Health() != HealthDegraded {
		t.Errorf("health must be degraded")
	}
}
