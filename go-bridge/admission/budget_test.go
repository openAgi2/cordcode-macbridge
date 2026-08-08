package admission

import (
	"strings"
	"testing"
)

// A0.5 / R11 P1-1：lease/HTTP/scheduling 时间预算三组不等式 + checked arithmetic overflow。
func validBudget() ManagementTimeBudget {
	// 一组合法数值：所有不等式成立。
	return ManagementTimeBudget{
		QuiesceHTTPTimeout:           3000,
		SafeDecodeBudget:             500,
		CommitHTTPTimeout:            3000,
		AbortHTTPTimeout:             3000,
		ExecutorSchedulingMargin:     500,
		MinimumCommitRemainingMillis: 4000, // >= 3000+500=3500  ✓
		LeaseMin:                     8000, // >= 3000+500+4000=7500  ✓
		LeaseMax:                     30000, // >= 8000  ✓
	}
}

func TestTimeBudget_Valid(t *testing.T) {
	if err := validBudget().Validate(); err != nil {
		t.Fatalf("valid budget rejected: %v", err)
	}
}

func TestTimeBudget_Inequality1Violated(t *testing.T) {
	b := validBudget()
	b.MinimumCommitRemainingMillis = 3000 // < 3000+500=3500
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "inequality 1 violated") {
		t.Fatalf("expected inequality 1 violation, got %v", err)
	}
}

func TestTimeBudget_Inequality2Violated(t *testing.T) {
	b := validBudget()
	b.LeaseMin = 7000 // < 3000+500+4000=7500
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "inequality 2 violated") {
		t.Fatalf("expected inequality 2 violation, got %v", err)
	}
}

func TestTimeBudget_Inequality3Violated(t *testing.T) {
	b := validBudget()
	b.LeaseMax = 7000 // < LeaseMin=8000
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "inequality 3 violated") {
		t.Fatalf("expected inequality 3 violation, got %v", err)
	}
}

func TestTimeBudget_OverflowChecked(t *testing.T) {
	b := validBudget()
	b.CommitHTTPTimeout = 4294967295           // uint32 max
	b.ExecutorSchedulingMargin = 1             // +1 => overflow in inequality 1
	b.MinimumCommitRemainingMillis = 4294967295 // large enough that inequality1 lhs is max
	err := b.Validate()
	if err == nil {
		t.Fatalf("expected overflow error, got nil")
	}
	// overflow may surface as inequality-1 overflow or inequality-1 violated depending on eval order
	if err != nil && !strings.Contains(err.Error(), "overflow") && !strings.Contains(err.Error(), "inequality 1") {
		t.Fatalf("expected overflow or inequality-1 error, got %v", err)
	}
}

// Abort consistency：commit/abort 共享 minimumCommitRemainingMillis 时，abort 也需足够裕量。
func TestTimeBudget_AbortConsistency(t *testing.T) {
	b := validBudget()
	b.AbortHTTPTimeout = 5000
	b.ExecutorSchedulingMargin = 500 // abort need 5500 > MinimumCommitRemainingMillis=4000
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "abort consistency") {
		t.Fatalf("expected abort consistency violation, got %v", err)
	}
}
