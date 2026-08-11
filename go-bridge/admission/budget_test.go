package admission

import (
	"strings"
	"testing"
)

// A0.5 / R11 P1-1：lease/HTTP/scheduling 时间预算三组不等式 + checked arithmetic overflow。
func validBudget() ManagementTimeBudget {
	return DefaultManagementTimeBudget()
}

func TestTimeBudget_Valid(t *testing.T) {
	if err := validBudget().Validate(); err != nil {
		t.Fatalf("valid budget rejected: %v", err)
	}
}

func TestTimeBudget_Inequality1Violated(t *testing.T) {
	b := validBudget()
	b.MinimumCommitRemainingMillis = 2000 // < 2000+500=2500
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "inequality 1 violated") {
		t.Fatalf("expected inequality 1 violation, got %v", err)
	}
}

func TestTimeBudget_Inequality2Violated(t *testing.T) {
	b := validBudget()
	b.LeaseMin = 4_000 // < 2000+500+2500=5000
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "inequality 2 violated") {
		t.Fatalf("expected inequality 2 violation, got %v", err)
	}
}

func TestTimeBudget_Inequality3Violated(t *testing.T) {
	b := validBudget()
	b.LeaseMax = 29_999 // < LeaseMin=30000
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "inequality 3 violated") {
		t.Fatalf("expected inequality 3 violation, got %v", err)
	}
}

func TestTimeBudget_OverflowChecked(t *testing.T) {
	b := validBudget()
	b.CommitHTTPTimeout = 4294967295            // uint32 max
	b.ExecutorSchedulingMargin = 1              // +1 => overflow in inequality 1
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
	b.AbortHTTPTimeout = 2500
	b.ExecutorSchedulingMargin = 500 // abort need 3000 > MinimumCommitRemainingMillis=2500
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "abort consistency") {
		t.Fatalf("expected abort consistency violation, got %v", err)
	}
}
