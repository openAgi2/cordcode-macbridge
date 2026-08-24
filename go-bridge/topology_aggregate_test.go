package gobridge

import "testing"

func TestAggregateDesktopTruthTable(t *testing.T) {
	shared := DesktopInstance{PID: 1, Classification: DesktopClassificationSharedOnly}
	private := DesktopInstance{PID: 2, Classification: DesktopClassificationPrivateOnly}
	dual := DesktopInstance{PID: 3, Classification: DesktopClassificationDual}
	unknown := DesktopInstance{PID: 4, Classification: DesktopClassificationUnresolved}
	emptyClass := DesktopInstance{PID: 5}

	cases := []struct {
		name      string
		instances []DesktopInstance
		want      TopologyAggregate
	}{
		{"no desktop instances", nil, AggregateDesktopAbsent},
		{"empty list", []DesktopInstance{}, AggregateDesktopAbsent},
		{"single shared_only", []DesktopInstance{shared}, AggregateAllShared},
		{"all shared_only", []DesktopInstance{shared, shared}, AggregateAllShared},
		{"single private_only", []DesktopInstance{private}, AggregateSplitPresent},
		{"private_only plus unresolved", []DesktopInstance{private, unknown}, AggregateSplitPresent},
		{"shared_only and private_only", []DesktopInstance{shared, private}, AggregateMixed},
		{"any dual", []DesktopInstance{dual, unknown}, AggregateMixed},
		{"dual plus shared_only", []DesktopInstance{dual, shared}, AggregateMixed},
		{"dual plus private_only", []DesktopInstance{dual, private}, AggregateMixed},
		{"shared plus private plus unresolved", []DesktopInstance{shared, private, unknown}, AggregateMixed},
		{"only unresolved", []DesktopInstance{unknown}, AggregateUnknown},
		{"shared_only plus unresolved", []DesktopInstance{shared, unknown}, AggregateUnknown},
		// 防御：未识别分类一律按 unresolved，不得默认健康。
		{"empty classification plus shared_only", []DesktopInstance{shared, emptyClass}, AggregateUnknown},
		{"only empty classification", []DesktopInstance{emptyClass}, AggregateUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AggregateDesktop(c.instances); got != c.want {
				t.Fatalf("AggregateDesktop(%d instances) = %q, want %q", len(c.instances), got, c.want)
			}
		})
	}
}

func TestDeriveSyncHealthFullTable(t *testing.T) {
	aggregates := []TopologyAggregate{
		AggregateDesktopAbsent, AggregateAllShared, AggregateMixed, AggregateSplitPresent, AggregateUnknown,
	}
	attachments := []BridgeAttachment{
		AttachmentShared, AttachmentPartial, AttachmentAbsent, AttachmentUnresolved,
	}
	// 按键构造映射，避免内层 switch 误写。
	wantByAttachment := map[BridgeAttachment]map[TopologyAggregate]SyncHealth{
		AttachmentShared: {
			AggregateAllShared:     SyncHealthy,
			AggregateDesktopAbsent: SyncNotApplicable,
			AggregateSplitPresent:  SyncDegraded,
			AggregateMixed:         SyncDegraded,
			AggregateUnknown:       SyncUnknown,
		},
		AttachmentPartial: {
			AggregateAllShared: SyncDegraded, AggregateDesktopAbsent: SyncDegraded,
			AggregateMixed: SyncDegraded, AggregateSplitPresent: SyncDegraded,
			AggregateUnknown: SyncDegraded,
		},
		AttachmentAbsent: {
			AggregateAllShared: SyncDegraded, AggregateDesktopAbsent: SyncDegraded,
			AggregateMixed: SyncDegraded, AggregateSplitPresent: SyncDegraded,
			AggregateUnknown: SyncDegraded,
		},
		AttachmentUnresolved: {
			AggregateAllShared: SyncUnknown, AggregateDesktopAbsent: SyncUnknown,
			AggregateMixed: SyncUnknown, AggregateSplitPresent: SyncUnknown,
			AggregateUnknown: SyncUnknown,
		},
	}
	for _, att := range attachments {
		for _, agg := range aggregates {
			want := wantByAttachment[att][agg]
			if got := DeriveSyncHealth(att, agg); got != want {
				t.Errorf("DeriveSyncHealth(%s, %s) = %q, want %q", att, agg, got, want)
			}
		}
	}
	// 防御：未知枚举一律 unknown，不默认 healthy/degraded。
	if got := DeriveSyncHealth("bogus", AggregateAllShared); got != SyncUnknown {
		t.Errorf("DeriveSyncHealth(bogus, all_shared) = %q, want unknown", got)
	}
	if got := DeriveSyncHealth(AttachmentShared, "bogus"); got != SyncUnknown {
		t.Errorf("DeriveSyncHealth(shared, bogus) = %q, want unknown", got)
	}
}

func TestDisplayFirstSamplePendingThenImmediate(t *testing.T) {
	s := NewTopologyDisplayState()
	if got := s.Displayed("x"); got != DisplaySamplePending {
		t.Fatalf("before first sample Displayed == %q, want sample_pending", got)
	}
	// 首个完成样本立即展示；首样本即 degraded 也立即展示（防抖只用于变化）。
	if got := s.Observe("x", string(SyncDegraded), false); got != string(SyncDegraded) {
		t.Fatalf("first completed sample = %q, want immediate degraded", got)
	}
	s2 := NewTopologyDisplayState()
	s2.Observe("x", string(SyncHealthy), false)
	if got := s2.Displayed("x"); got != string(SyncHealthy) {
		t.Fatalf("first completed sample = %q, want immediate healthy", got)
	}
}

func TestDisplayDegradeNeedsTwoConsecutive(t *testing.T) {
	s := NewTopologyDisplayState()
	s.Observe("x", string(SyncHealthy), false)
	if got := s.Observe("x", string(SyncDegraded), false); got != string(SyncHealthy) {
		t.Fatalf("first degrade sample must not flip yet: %q, want healthy", got)
	}
	// 中间漂移回 healthy 会取消 degrade 窗口。
	if got := s.Observe("x", string(SyncHealthy), false); got != string(SyncHealthy) {
		t.Fatalf("drift sample = %q, want healthy", got)
	}
	if got := s.Observe("x", string(SyncDegraded), false); got != string(SyncHealthy) {
		t.Fatalf("degrade after drift must restart window: %q, want healthy", got)
	}
	if got := s.Observe("x", string(SyncDegraded), false); got != string(SyncDegraded) {
		t.Fatalf("second consecutive degrade must flip: %q, want degraded", got)
	}
}

func TestDisplayRecoveryNeedsThreeConsecutive(t *testing.T) {
	s := NewTopologyDisplayState()
	s.Observe("x", string(SyncDegraded), false)
	s.Observe("x", string(SyncDegraded), false)
	if got := s.Observe("x", string(SyncHealthy), false); got != string(SyncDegraded) {
		t.Fatalf("recovery sample 1 must not flip: %q, want degraded", got)
	}
	if got := s.Observe("x", string(SyncHealthy), false); got != string(SyncDegraded) {
		t.Fatalf("recovery sample 2 must not flip: %q, want degraded", got)
	}
	if got := s.Observe("x", string(SyncHealthy), false); got != string(SyncHealthy) {
		t.Fatalf("recovery sample 3 must flip: %q, want healthy", got)
	}
}

func TestDisplayUncertainImmediate(t *testing.T) {
	s := NewTopologyDisplayState()
	s.Observe("x", string(SyncHealthy), false)
	// 任何证据不足一律立即展示（不防抖）。
	if got := s.Observe("x", string(SyncUnknown), false); got != string(SyncUnknown) {
		t.Fatalf("unknown must be immediate: %q", got)
	}
	if got := s.Observe("x", DisplayUncertain, false); got != DisplayUncertain {
		t.Fatalf("unresolved must be immediate: %q", got)
	}
	// 恢复采样失败保持 unresolved。
	if got := s.Observe("x", DisplayUncertain, false); got != DisplayUncertain {
		t.Fatalf("recovery failure must stay unresolved: %q", got)
	}
	// 从不确定态恢复：单一完成样本即正证据，立即展示（不是 N=3）。
	if got := s.Observe("x", string(SyncHealthy), false); got != string(SyncHealthy) {
		t.Fatalf("uncertain to certain must be immediate: %q", got)
	}
}

func TestDisplayDesktopAbsentImmediate(t *testing.T) {
	s := NewTopologyDisplayState()
	s.Observe("x", string(SyncHealthy), false)
	if got := s.Observe("x", string(AggregateDesktopAbsent), false); got != string(AggregateDesktopAbsent) {
		t.Fatalf("desktop_absent must be immediate: %q", got)
	}
	// 离开 desktop_absent 属确定→确定变化：N=3。
	s.Observe("x", string(AggregateAllShared), false)
	if got := s.Observe("x", string(AggregateAllShared), false); got != string(AggregateDesktopAbsent) {
		t.Fatalf("desktop_absent -> all_shared sample 2 must not flip: %q", got)
	}
	if got := s.Observe("x", string(AggregateAllShared), false); got != string(AggregateAllShared) {
		t.Fatalf("desktop_absent -> all_shared sample 3 must flip: %q", got)
	}
}

func TestDisplayStaleBecomesUnresolvedImmediate(t *testing.T) {
	s := NewTopologyDisplayState()
	s.Observe("x", string(SyncHealthy), false)
	// 过期 = 证据不足，立即展示 unresolved。
	if got := s.Observe("x", string(SyncHealthy), true); got != DisplayUncertain {
		t.Fatalf("stale sample must show unresolved: %q", got)
	}
	// 恢复再采到正证据 → 立即恢复。
	if got := s.Observe("x", string(SyncHealthy), false); got != string(SyncHealthy) {
		t.Fatalf("fresh positive after stale must be immediate: %q", got)
	}
}

func TestDisplayKeyIndependenceAndReset(t *testing.T) {
	s := NewTopologyDisplayState()
	s.Observe("a", string(SyncHealthy), false)
	s.Observe("b", string(SyncHealthy), false)
	// b 进入 degrade 窗口（尚未翻转），a 保持 healthy。
	if got := s.Observe("b", string(SyncDegraded), false); got != string(SyncHealthy) {
		t.Fatalf("b pending should stay healthy: %q", got)
	}
	if got := s.Displayed("a"); got != string(SyncHealthy) {
		t.Fatalf("a must not be affected by b: %q", got)
	}
	// Reset 后所有维度回到 sample_pending，下一采样按首次处理立即展示。
	s.Reset()
	if got := s.Displayed("b"); got != DisplaySamplePending {
		t.Fatalf("after reset b Displayed == %q, want sample_pending", got)
	}
	if got := s.Observe("b", string(SyncDegraded), false); got != string(SyncDegraded) {
		t.Fatalf("first sample after reset must be immediate: %q", got)
	}
	if got := s.Displayed("a"); got != DisplaySamplePending {
		t.Fatalf("after reset a Displayed == %q, want sample_pending", got)
	}
}

func TestDisplayDriftCancelsPendingWindow(t *testing.T) {
	s := NewTopologyDisplayState()
	s.Observe("x", string(SyncHealthy), false)
	s.Observe("x", string(SyncDegraded), false) // pending degraded(1)
	// 漂移到其它确定值：旧窗口作废，新目标按 N=3 计数。
	s.Observe("x", string(SyncNotApplicable), false)
	if got := s.Observe("x", string(SyncNotApplicable), false); got != string(SyncHealthy) {
		t.Fatalf("drift target sample 2 must not flip a healthy->not_applicable change: %q", got)
	}
	if got := s.Observe("x", string(SyncNotApplicable), false); got != string(SyncNotApplicable) {
		t.Fatalf("drift target sample 3 must flip: %q", got)
	}
}
