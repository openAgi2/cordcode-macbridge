package grokbuild

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestDriverRailExitPlanModeSurfacesPlanCard: iOS-originated /plan turns
// run on the --no-leader actor. The agent addresses x.ai/exit_plan_mode to
// that actor as its sole ACP client. Dropping the request (pre-fix default
// branch) left grok awaiting_plan_approval with no iPhone card.
func TestDriverRailExitPlanModeSurfacesPlanCard(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	t.Cleanup(func() {
		_ = inW.Close()
		_ = inR.Close()
		_ = outW.Close()
		_ = outR.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &grokSession{
		stdin:        inW,
		stdout:       outR,
		events:       make(chan core.Event, 16),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		pendingPlans: make(map[string]pendingDriverPlan),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	s.alive.Store(true)
	s.sessionID.Store("sess-plan")
	go s.readLoop()

	req := []byte(`{"jsonrpc":"2.0","id":9,"method":"_x.ai/exit_plan_mode","params":{"sessionId":"sess-plan","toolCallId":"call_plan_9","planContent":"# 太阳系短故事\n\n## 步骤\n1. 写故事"}}` + "\n")
	if _, err := outW.Write(req); err != nil {
		t.Fatal(err)
	}

	var ev core.Event
	select {
	case ev = <-s.events:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for plan_review card")
	}
	if ev.Type != core.EventPermissionRequest {
		t.Fatalf("type = %v, want permission_request", ev.Type)
	}
	if ev.RequestID != "9" {
		t.Fatalf("RequestID = %q, want 9", ev.RequestID)
	}
	if ev.PermissionKind != "plan_review" {
		t.Fatalf("PermissionKind = %q, want plan_review", ev.PermissionKind)
	}
	if ev.ToolName != "计划审批: 太阳系短故事" {
		t.Fatalf("ToolName = %q", ev.ToolName)
	}
	if ev.PlanReview == nil || ev.PlanReview.Content == "" {
		t.Fatalf("PlanReview missing content: %+v", ev.PlanReview)
	}

	// Approve must write the official exit_plan_mode result, not a
	// session/request_permission outcome envelope.
	done := make(chan []byte, 1)
	go func() {
		sc := bufio.NewScanner(inR)
		if sc.Scan() {
			done <- append([]byte(nil), sc.Bytes()...)
		} else {
			done <- nil
		}
	}()
	if err := s.RespondPermission("9", core.PermissionResult{PlanAction: "approve", Behavior: "allow"}); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	var raw []byte
	select {
	case raw = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ACP answer")
	}
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("answer json: %v (%s)", err, raw)
	}
	if fmtID := fmtSprintID(resp["id"]); fmtID != "9" {
		t.Fatalf("answer id = %v, want 9", resp["id"])
	}
	result, _ := resp["result"].(map[string]any)
	if result["outcome"] != "approved" {
		t.Fatalf("outcome = %v, want approved (not request_permission selected)", result)
	}
}

func fmtSprintID(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.Itoa(int(n))
	case json.Number:
		return n.String()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
