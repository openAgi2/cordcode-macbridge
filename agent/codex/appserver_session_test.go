package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestAppServerSession_CommandArgsDefaultOmitsListen(t *testing.T) {
	s := &appServerSession{}
	args := s.commandArgs()

	want := []string{"app-server"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d, args=%v", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q, args=%v", i, args[i], want[i], args)
		}
	}
}

func TestAppServerSession_CommandArgs_AlwaysStdio(t *testing.T) {
	s := &appServerSession{url: "ws://127.0.0.1:3845"}
	args := s.commandArgs()

	want := []string{"app-server"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d, args=%v", len(args), len(want), args)
	}
	if args[0] != want[0] {
		t.Fatalf("args[0] = %q, want %q", args[0], want[0])
	}
}

func TestAppServerSession_MultipleDefaultSessionsAvoidSharedListenAddress(t *testing.T) {
	sessions := []*appServerSession{{}, {}}
	for i, session := range sessions {
		args := session.commandArgs()
		for _, arg := range args {
			if arg == "--listen" {
				t.Fatalf("session %d args = %v, want no --listen for default app-server session", i, args)
			}
		}
		if len(args) != 1 || args[0] != "app-server" {
			t.Fatalf("session %d args = %v, want [app-server]", i, args)
		}
	}
}

func TestAppServerSessionTransport_ExplicitURLUsesWebSocket(t *testing.T) {
	if got := appServerSessionTransport(true); got != appServerTransportWebSocket {
		t.Fatalf("explicit URL transport = %q, want %q", got, appServerTransportWebSocket)
	}
	if got := appServerSessionTransport(false); got != appServerTransportStdio {
		t.Fatalf("implicit/default URL transport = %q, want %q", got, appServerTransportStdio)
	}
}

func TestAgentWorkspaceOptions_OnlyExportsExplicitAppServerURL(t *testing.T) {
	implicit := &Agent{
		backend:      "app_server",
		appServerURL: "ws://127.0.0.1:3845",
	}
	if opts := implicit.WorkspaceAgentOptions(); opts["app_server_url"] != nil {
		t.Fatalf("implicit app_server_url exported: %#v", opts["app_server_url"])
	}

	explicit := &Agent{
		backend:         "app_server",
		appServerURL:    "ws://127.0.0.1:9999",
		appServerURLSet: true,
	}
	if got := explicit.WorkspaceAgentOptions()["app_server_url"]; got != "ws://127.0.0.1:9999" {
		t.Fatalf("explicit app_server_url = %#v, want ws://127.0.0.1:9999", got)
	}
}

func TestAppServerSession_ConnectWebSocketUsesExistingServer(t *testing.T) {
	var upgrader websocket.Upgrader
	connected := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connected <- struct{}{}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &appServerSession{
		url:       "ws" + server.URL[len("http"):],
		transport: appServerTransportWebSocket,
		ctx:       ctx,
		cancel:    cancel,
		events:    make(chan core.Event, 1),
		pending:   make(map[int64]chan rpcResponseEnvelope),
	}
	s.alive.Store(true)

	if err := s.connect(); err != nil {
		t.Fatalf("connect websocket: %v", err)
	}
	defer s.Close()

	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("websocket server did not receive connection")
	}
	if s.conn == nil {
		t.Fatal("conn is nil after websocket connect")
	}
	if s.cmd != nil {
		t.Fatalf("cmd = %#v, want nil in websocket mode", s.cmd)
	}
	if s.stdin != nil {
		t.Fatalf("stdin = %#v, want nil in websocket mode", s.stdin)
	}
}

func TestAppServerSession_ConnectWebSocketFailureDoesNotStartStdio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not a websocket", http.StatusBadRequest)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &appServerSession{
		url:       "ws" + server.URL[len("http"):],
		transport: appServerTransportWebSocket,
		ctx:       ctx,
		cancel:    cancel,
		events:    make(chan core.Event, 1),
		pending:   make(map[int64]chan rpcResponseEnvelope),
	}
	s.alive.Store(true)

	if err := s.connect(); err == nil {
		t.Fatal("connect websocket unexpectedly succeeded")
	}
	if s.conn != nil {
		t.Fatalf("conn = %#v, want nil after websocket failure", s.conn)
	}
	if s.cmd != nil {
		t.Fatalf("cmd = %#v, want nil; websocket failure must not start stdio", s.cmd)
	}
	if s.stdin != nil {
		t.Fatalf("stdin = %#v, want nil; websocket failure must not start stdio", s.stdin)
	}
}

func TestAppServerModeSettings_MatchesCodexPresets(t *testing.T) {
	approval, sandbox := appServerModeSettings("default")
	if approval != "on-request" {
		t.Fatalf("default approval = %q, want on-request", approval)
	}
	if sandbox != "workspace-write" {
		t.Fatalf("default sandbox = %q, want workspace-write", sandbox)
	}

	approval, sandbox = appServerModeSettings("auto-review")
	if approval != "on-failure" {
		t.Fatalf("auto-review approval = %q, want on-failure", approval)
	}
	if sandbox != "workspace-write" {
		t.Fatalf("auto-review sandbox = %q, want workspace-write", sandbox)
	}

	approval, sandbox = appServerModeSettings("full-access")
	if approval != "never" {
		t.Fatalf("full-access approval = %q, want never", approval)
	}
	if sandbox != "danger-full-access" {
		t.Fatalf("full-access sandbox = %q, want danger-full-access", sandbox)
	}

	approval, sandbox = appServerModeSettings("custom")
	if approval != "" || sandbox != "" {
		t.Fatalf("custom settings = (%q, %q), want empty overrides", approval, sandbox)
	}
}

func TestAppServerSession_ApplyThreadRuntimeState(t *testing.T) {
	s := &appServerSession{}
	effort := "xhigh"

	s.applyThreadRuntimeState("/tmp/project", "gpt-5.4", &effort)

	if got := s.GetWorkDir(); got != "/tmp/project" {
		t.Fatalf("GetWorkDir() = %q, want /tmp/project", got)
	}
	if got := s.GetModel(); got != "gpt-5.4" {
		t.Fatalf("GetModel() = %q, want gpt-5.4", got)
	}
	if got := s.GetReasoningEffort(); got != "xhigh" {
		t.Fatalf("GetReasoningEffort() = %q, want xhigh", got)
	}
}

func TestAppServerSession_HandleRateLimitsUpdatedCachesUsage(t *testing.T) {
	s := &appServerSession{}
	raw, err := json.Marshal(appServerRateLimitsResponse{
		RateLimits: appServerRateLimitSnapshot{
			LimitID:   "codex",
			PlanType:  "pro",
			Primary:   &appServerRateLimitWindow{UsedPercent: 25, WindowDurationMins: 15, ResetsAt: 1730947200},
			Secondary: &appServerRateLimitWindow{UsedPercent: 42, WindowDurationMins: 60, ResetsAt: 1730950800},
			Credits:   &appServerCreditsSnapshot{HasCredits: true, Unlimited: false},
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}

	s.handleNotification("account/rateLimits/updated", raw)

	report, err := s.GetUsage(context.Background())
	if err != nil {
		t.Fatalf("GetUsage() returned error: %v", err)
	}
	if report.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", report.Provider)
	}
	if report.Plan != "pro" {
		t.Fatalf("plan = %q, want pro", report.Plan)
	}
	if len(report.Buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(report.Buckets))
	}
	if got := report.Buckets[0].Name; got != "codex" {
		t.Fatalf("bucket name = %q, want codex", got)
	}
	if got := report.Buckets[0].Windows[0].WindowSeconds; got != 15*60 {
		t.Fatalf("primary window seconds = %d, want %d", got, 15*60)
	}
	if got := report.Buckets[0].Windows[1].UsedPercent; got != 42 {
		t.Fatalf("secondary used percent = %d, want 42", got)
	}
	if report.Credits == nil || !report.Credits.HasCredits {
		t.Fatalf("credits = %#v, want has credits", report.Credits)
	}
}

func TestAppServerSession_HandleThreadTokenUsageUpdatedCachesAndEmitsContextUsage(t *testing.T) {
	s := &appServerSession{events: make(chan core.Event, 1)}
	raw, err := json.Marshal(appServerThreadTokenUsageNotification{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		TokenUsage: struct {
			Total              codexTokenUsage `json:"total"`
			Last               codexTokenUsage `json:"last"`
			ModelContextWindow int             `json:"modelContextWindow"`
		}{
			Total: codexTokenUsage{
				TotalTokens:           52011395,
				InputTokens:           51847383,
				CachedInputTokens:     48187904,
				OutputTokens:          164012,
				ReasoningOutputTokens: 78910,
			},
			Last: codexTokenUsage{
				TotalTokens:           41061,
				InputTokens:           40849,
				CachedInputTokens:     36864,
				OutputTokens:          212,
				ReasoningOutputTokens: 32,
			},
			ModelContextWindow: 258400,
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}

	s.handleNotification("thread/tokenUsage/updated", raw)

	usage := s.GetContextUsage()
	if usage == nil {
		t.Fatal("GetContextUsage() = nil, want cached context usage")
	}
	if usage.UsedTokens != 41061 {
		t.Fatalf("used tokens = %d, want 41061", usage.UsedTokens)
	}
	if usage.BaselineTokens != codexContextBaselineTokens {
		t.Fatalf("baseline tokens = %d, want %d", usage.BaselineTokens, codexContextBaselineTokens)
	}
	if usage.TotalTokens != 41061 {
		t.Fatalf("total tokens = %d, want 41061", usage.TotalTokens)
	}
	if usage.ContextWindow != 258400 {
		t.Fatalf("context window = %d, want 258400", usage.ContextWindow)
	}
	if usage.CachedInputTokens != 36864 {
		t.Fatalf("cached input tokens = %d, want 36864", usage.CachedInputTokens)
	}
	if usage.InputTokens != 40849 {
		t.Fatalf("input tokens = %d, want 40849", usage.InputTokens)
	}

	select {
	case event := <-s.events:
		if event.Type != core.EventContextUsageUpdated {
			t.Fatalf("event.Type = %q, want %q", event.Type, core.EventContextUsageUpdated)
		}
		if event.SessionID != "thread-1" {
			t.Fatalf("event.SessionID = %q, want thread-1", event.SessionID)
		}
		if event.ContextUsage == nil {
			t.Fatal("event.ContextUsage = nil, want context usage")
		}
		if event.ContextUsage.UsedTokens != 41061 {
			t.Fatalf("event.ContextUsage.UsedTokens = %d, want 41061", event.ContextUsage.UsedTokens)
		}
	default:
		t.Fatal("expected EventContextUsageUpdated to be emitted")
	}
}

func TestAppServerSession_HandlePlanUpdatedEmitsEventPlan(t *testing.T) {
	s := &appServerSession{events: make(chan core.Event, 1)}
	raw, err := json.Marshal(planNotification{
		ThreadID: "thread-plan",
		TurnID:   "turn-plan",
		Plan: []codexPlanEntry{
			{Step: "第一步", Status: "completed"},
			{Step: "第二步", Status: "inProgress"},
			{Step: "第三步", Status: "pending"},
			{Step: "第四步", Status: "canceled"},
			{Step: "", Status: "completed"},
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}

	s.handleNotification("turn/plan/updated", raw)

	select {
	case event := <-s.events:
		if event.Type != core.EventPlan {
			t.Fatalf("event.Type = %q, want %q", event.Type, core.EventPlan)
		}
		if event.SessionID != "thread-plan" {
			t.Fatalf("event.SessionID = %q, want thread-plan", event.SessionID)
		}
		if len(event.Plan) != 4 {
			t.Fatalf("len(event.Plan) = %d, want 4", len(event.Plan))
		}
		wantStatuses := []string{"completed", "in_progress", "pending", "cancelled"}
		for i, want := range wantStatuses {
			if got := event.Plan[i].Status; got != want {
				t.Fatalf("event.Plan[%d].Status = %q, want %q", i, got, want)
			}
			if got := event.Plan[i].Priority; got != "normal" {
				t.Fatalf("event.Plan[%d].Priority = %q, want normal", i, got)
			}
		}
	default:
		t.Fatal("expected EventPlan to be emitted")
	}
}

func TestAppServerSession_HandlePlanUpdatedEmitsSequentialSnapshots(t *testing.T) {
	s := &appServerSession{events: make(chan core.Event, 2)}
	rawFirst, err := json.Marshal(planNotification{
		ThreadID: "thread-plan",
		TurnID:   "turn-1",
		Plan: []codexPlanEntry{
			{Step: "第一步", Status: "pending"},
			{Step: "第二步", Status: "inProgress"},
		},
	})
	if err != nil {
		t.Fatalf("marshal first notification: %v", err)
	}
	rawSecond, err := json.Marshal(planNotification{
		ThreadID: "thread-plan",
		TurnID:   "turn-2",
		Plan: []codexPlanEntry{
			{Step: "第一步", Status: "completed"},
			{Step: "第二步", Status: "blocked"},
			{Step: "第三步", Status: "canceled"},
		},
	})
	if err != nil {
		t.Fatalf("marshal second notification: %v", err)
	}

	s.handleNotification("turn/plan/updated", rawFirst)
	s.handleNotification("turn/plan/updated", rawSecond)

	first := <-s.events
	second := <-s.events
	if first.Type != core.EventPlan || second.Type != core.EventPlan {
		t.Fatalf("event types = %q, %q; want %q, %q", first.Type, second.Type, core.EventPlan, core.EventPlan)
	}
	if len(first.Plan) != 2 {
		t.Fatalf("len(first.Plan) = %d, want 2", len(first.Plan))
	}
	if got := first.Plan[1].Status; got != "in_progress" {
		t.Fatalf("first second status = %q, want in_progress", got)
	}
	if len(second.Plan) != 3 {
		t.Fatalf("len(second.Plan) = %d, want 3", len(second.Plan))
	}
	if got := second.Plan[0].Status; got != "completed" {
		t.Fatalf("second first status = %q, want completed", got)
	}
	if got := second.Plan[1].Status; got != "pending" {
		t.Fatalf("unknown status fallback = %q, want pending", got)
	}
	if got := second.Plan[2].Status; got != "cancelled" {
		t.Fatalf("cancelled status = %q, want cancelled", got)
	}
}

func TestMapAppServerRateLimits_PrefersMultiBucketView(t *testing.T) {
	report := mapAppServerRateLimits(appServerRateLimitsResponse{
		RateLimits: appServerRateLimitSnapshot{
			LimitID:  "legacy",
			PlanType: "team",
			Primary:  &appServerRateLimitWindow{UsedPercent: 99, WindowDurationMins: 15},
		},
		RateLimitsByLimitID: map[string]appServerRateLimitSnapshot{
			"codex": {
				LimitID:   "codex",
				LimitName: "Codex",
				PlanType:  "team",
				Primary:   &appServerRateLimitWindow{UsedPercent: 10, WindowDurationMins: 15},
			},
			"codex_other": {
				LimitID:  "codex_other",
				PlanType: "team",
				Primary:  &appServerRateLimitWindow{UsedPercent: 20, WindowDurationMins: 60},
			},
		},
	})

	if report.Plan != "team" {
		t.Fatalf("plan = %q, want team", report.Plan)
	}
	if len(report.Buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(report.Buckets))
	}
	if report.Buckets[0].Name != "Codex" {
		t.Fatalf("first bucket = %q, want Codex", report.Buckets[0].Name)
	}
	if report.Buckets[1].Name != "codex_other" {
		t.Fatalf("second bucket = %q, want codex_other", report.Buckets[1].Name)
	}
}

var _ interface {
	GetUsage(context.Context) (*core.UsageReport, error)
} = (*appServerSession)(nil)

var _ interface {
	GetContextUsage() *core.ContextUsage
} = (*appServerSession)(nil)
