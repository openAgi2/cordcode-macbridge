package gobridge

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type catalogContextKey struct{}

func TestNativeMembershipPropagatesContextAndSharesVisibility(t *testing.T) {
	withCodexRootsDisabled(t)
	workspace := t.TempDir()
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), catalogContextKey{}, "caller"), time.Second)
	defer cancel()

	codexBase := &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "disk-must-not-run"}}}
	codex := &fakeCodexCatalogAgent{fakeAgent: codexBase, fetchFn: func(got context.Context, dir string) ([]core.AgentSessionInfo, error) {
		if got.Value(catalogContextKey{}) != "caller" {
			t.Fatal("Codex native fetch lost caller context")
		}
		deadline, ok := got.Deadline()
		if !ok {
			t.Fatal("Codex native fetch lost deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > catalogRequestTimeout {
			t.Fatalf("Codex native fetch deadline remaining=%s want (0,%s]", remaining, catalogRequestTimeout)
		}
		return []core.AgentSessionInfo{
			{ID: "visible", Summary: "visible", Directory: workspace},
			{ID: "ghost", Summary: "ghost"},
		}, nil
	}}
	h := newTestHandlers(t)
	h.RegisterAgent("codex", codex)
	membership, _, err := h.codexVisibleMembership(ctx, "codex", "")
	if err != nil || len(membership) != 1 || membership[0]["id"] != "visible" {
		t.Fatalf("Codex membership=%#v err=%v", membership, err)
	}
	if codexBase.ListSessionsCallCount() != 0 {
		t.Fatal("Codex native membership called disk-scan ListSessions")
	}

	grokBase := &fakeAgent{name: "grokbuild", sessionInfos: []core.AgentSessionInfo{{ID: "disk-must-not-run"}}}
	grok := &fakeGrokCatalogAgent{fakeAgent: grokBase, fetchFn: func(got context.Context) ([]core.AgentSessionInfo, error) {
		if got.Value(catalogContextKey{}) != "caller" {
			t.Fatal("Grok native fetch lost caller context")
		}
		return []core.AgentSessionInfo{
			{ID: "visible", Summary: "real title", Directory: workspace},
			{ID: "placeholder", Summary: "", Directory: workspace},
		}, nil
	}}
	h.RegisterAgent("grokbuild", grok)
	membership, _, err = h.grokVisibleMembership(ctx, "grokbuild")
	if err != nil || len(membership) != 1 || membership[0]["id"] != "visible" {
		t.Fatalf("Grok membership=%#v err=%v", membership, err)
	}
	if grokBase.ListSessionsCallCount() != 0 {
		t.Fatal("Grok native membership called disk-scan ListSessions")
	}
}

func TestDiscoveryFingerprintUsesNativeMembershipWithoutEnrichmentOrDiskScan(t *testing.T) {
	withCodexRootsDisabled(t)
	workspace := t.TempDir()
	base := &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "disk-must-not-run"}}}
	native := []core.AgentSessionInfo{{ID: "native", Summary: "title-a", Directory: workspace, ModifiedAt: time.Unix(1, 0)}}
	agent := &fakeCodexCatalogAgent{fakeAgent: base, fetchFn: func(context.Context, string) ([]core.AgentSessionInfo, error) {
		return append([]core.AgentSessionInfo(nil), native...), nil
	}}
	h := newTestHandlers(t)
	h.RegisterAgent("codex", agent)
	first, count, _, err := h.discoveryFingerprint(context.Background(), "codex", agent)
	if err != nil || count != 1 || first == "" {
		t.Fatalf("first fingerprint=%q count=%d err=%v", first, count, err)
	}
	native[0].Summary = "title-b"
	second, _, _, err := h.discoveryFingerprint(context.Background(), "codex", agent)
	if err != nil || second == first {
		t.Fatalf("title-only native change did not rotate fingerprint: first=%q second=%q err=%v", first, second, err)
	}
	if base.ListSessionsCallCount() != 0 {
		t.Fatal("discovery called disk-scan ListSessions")
	}

	grokBase := &fakeAgent{name: "grokbuild", sessionInfos: []core.AgentSessionInfo{{ID: "disk-must-not-run"}}}
	grokNative := []core.AgentSessionInfo{{ID: "grok-native", Summary: "visible", Directory: workspace, ModifiedAt: time.Unix(1, 0)}}
	grok := &fakeGrokCatalogAgent{fakeAgent: grokBase, fetchFn: func(context.Context) ([]core.AgentSessionInfo, error) {
		return append([]core.AgentSessionInfo(nil), grokNative...), nil
	}}
	h.RegisterAgent("grokbuild", grok)
	if got, count, _, err := h.discoveryFingerprint(context.Background(), "grokbuild", grok); err != nil || count != 1 || got == "" {
		t.Fatalf("Grok fingerprint=%q count=%d err=%v", got, count, err)
	}
	if grokBase.ListSessionsCallCount() != 0 {
		t.Fatal("Grok discovery called disk-scan ListSessions")
	}
}

func TestListSemanticFingerprintCoversFrozenTupleAndIgnoresPresentation(t *testing.T) {
	base := []map[string]interface{}{
		{"id": "a", "updatedAtMillis": int64(2), "title": "A", "directory": "/tmp/a/../workspace", "projectId": "p"},
		{"id": "b", "updatedAtMillis": int64(1), "title": "B", "directory": "/tmp/workspace", "projectId": "p"},
	}
	fingerprint := listSemanticFingerprint(base)
	mutations := []func([]map[string]interface{}){
		func(m []map[string]interface{}) { m[0]["updatedAtMillis"] = int64(3) },
		func(m []map[string]interface{}) { m[0]["title"] = "changed" },
		func(m []map[string]interface{}) { m[0]["directory"] = "/tmp/other" },
		func(m []map[string]interface{}) { m[0]["projectId"] = "other" },
		func(m []map[string]interface{}) { m[0], m[1] = m[1], m[0] },
	}
	for index, mutate := range mutations {
		copyMaps := []map[string]interface{}{mapsClone(base[0]), mapsClone(base[1])}
		mutate(copyMaps)
		if got := listSemanticFingerprint(copyMaps); got == fingerprint {
			t.Fatalf("tuple mutation %d did not rotate fingerprint", index)
		}
	}
	presentation := []map[string]interface{}{mapsClone(base[0]), mapsClone(base[1])}
	presentation[0]["pinned"] = true
	presentation[0]["running"] = true
	if got := listSemanticFingerprint(presentation); got != fingerprint {
		t.Fatal("pin/running-only presentation state changed membership fingerprint")
	}
	scope := codexCatalogScopeKey("codex", "/tmp/workspace")
	cache, _ := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	calls := 0
	snap, err := cache.FetchOrReuse(scope, builderFromMaps(base, &calls))
	if err != nil {
		t.Fatal(err)
	}
	if want := deriveSnapshotEpoch(scope.identity() + "\x00" + fingerprint); snap.epoch != want {
		t.Fatalf("snapshot epoch=%q want shared tuple fingerprint epoch=%q", snap.epoch, want)
	}
}

func mapsClone(input map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

// TestListOrderFingerprintIgnoresUpdatedAtChurn：head 提示指纹只覆盖顺序+id——
// 成员 updatedAt（流式 turn 中随 delta 变化）不得改变指纹，变更的只有成员/顺序。
func TestListOrderFingerprintIgnoresUpdatedAtChurn(t *testing.T) {
	base := []map[string]interface{}{
		{"id": "a", "updatedAtMillis": int64(2), "title": "A", "directory": "/tmp/workspace"},
		{"id": "b", "updatedAtMillis": int64(1), "title": "B", "directory": "/tmp/workspace"},
	}
	fingerprint := listOrderFingerprint(base)

	churn := []map[string]interface{}{mapsClone(base[0]), mapsClone(base[1])}
	churn[0]["updatedAtMillis"] = int64(3)
	churn[0]["title"] = "changed"
	churn[1]["updatedAtMillis"] = int64(4)
	if got := listOrderFingerprint(churn); got != fingerprint {
		t.Fatal("updatedAt/title churn must not change head order fingerprint")
	}

	reordered := []map[string]interface{}{mapsClone(base[1]), mapsClone(base[0])}
	if got := listOrderFingerprint(reordered); got == fingerprint {
		t.Fatal("recency reorder must change head order fingerprint")
	}

	added := []map[string]interface{}{mapsClone(base[0])}
	if got := listOrderFingerprint(added); got == fingerprint {
		t.Fatal("member set change must change head order fingerprint")
	}
}

func TestNativeV2SameTimestampPagination(t *testing.T) {
	for _, backend := range []string{"codex", "grokbuild"} {
		for _, scope := range []string{"directory"} {
			t.Run(backend+"/"+scope, func(t *testing.T) {
				workspace := t.TempDir()
				if backend == "codex" {
					withCodexRootsDisabled(t)
				}
				infos := []core.AgentSessionInfo{
					{ID: "c", Summary: "C", Directory: workspace, ModifiedAt: time.Unix(1, 0)},
					{ID: "a", Summary: "A", Directory: workspace, ModifiedAt: time.Unix(1, 0)},
					{ID: "b", Summary: "B", Directory: workspace, ModifiedAt: time.Unix(1, 0)},
				}
				h := newTestHandlers(t)
				if backend == "codex" {
					h.RegisterAgent(backend, &fakeCodexCatalogAgent{fakeAgent: &fakeAgent{name: backend}, fetchFn: func(context.Context, string) ([]core.AgentSessionInfo, error) { return infos, nil }})
				} else {
					h.RegisterAgent(backend, &fakeGrokCatalogAgent{fakeAgent: &fakeAgent{name: backend}, fetchFn: func(context.Context) ([]core.AgentSessionInfo, error) { return infos, nil }})
				}
				server, client, cleanup := openTestConn(t)
				defer cleanup()
				agent, ok := h.getAgent(backend)
				if !ok {
					t.Fatal("registered catalog agent missing")
				}
				h.eventPublisher.SetConnCatalogCursorEpochV2(server, true)
				cursor := ""
				var got []string
				for page := 0; page < 3; page++ {
					params := map[string]any{"limit": 1}
					if scope == "directory" {
						params["directory"] = workspace
					}
					if cursor != "" {
						params["cursor"] = cursor
					}
					h.handleListSessions(server, WireMessage{BackendID: backend, Method: "list_sessions", RequestID: backend, Params: mustJSONRaw(t, params)}, agent)
					msg := readJSONMaps(t, client, 1)[0]
					got = append(got, resultSessionIDs(t, msg)...)
					data := msg["data"].(map[string]any)
					cursor, _ = data["nextCursor"].(string)
					if cursor != "" {
						if _, isV1, err := decodeListCursorV2(cursor); err != nil || isV1 {
							t.Fatalf("page %d cursor isV1=%v err=%v, want v2", page, isV1, err)
						}
					}
				}
				want := []string{"c", "a", "b"}
				if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
					t.Fatalf("v2 pages=%v want=%v", got, want)
				}
			})
		}
	}
}

func TestNativeListAndPollerPropagateCancellationAndDeadline(t *testing.T) {
	root, cancelRoot := context.WithCancel(context.Background())
	h := NewHandlersWithContext(root)
	t.Cleanup(func() {
		cancelRoot()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Shutdown(shutdownCtx)
	})
	sawListDeadline := false
	var listDeadlineRemaining time.Duration
	base := &fakeAgent{name: "codex"}
	agent := &fakeCodexCatalogAgent{fakeAgent: base, fetchFn: func(ctx context.Context, _ string) ([]core.AgentSessionInfo, error) {
		deadline, ok := ctx.Deadline()
		sawListDeadline = ok
		listDeadlineRemaining = time.Until(deadline)
		return nil, ctx.Err()
	}}
	h.RegisterAgent("codex", agent)
	server, client, cleanup := openTestConn(t)
	defer cleanup()
	h.eventPublisher.SetConnCatalogCursorEpochV2(server, true)
	cancelRoot()
	h.HandleRPC(server, WireMessage{BackendID: "codex", Method: "list_sessions", RequestID: "canceled-list", Params: mustJSONRaw(t, map[string]any{})})
	msg := readJSONMaps(t, client, 1)[0]
	if !sawListDeadline || listDeadlineRemaining <= 0 || listDeadlineRemaining > catalogRequestTimeout || msg["ok"] != false || msg["error"].(map[string]any)["code"] != "list_failed" {
		t.Fatalf("root-canceled list did not reach native deadline: deadline=%v remaining=%s msg=%#v", sawListDeadline, listDeadlineRemaining, msg)
	}

	pollCtx, cancelPoll := context.WithCancel(context.Background())
	cancelPoll()
	sawPollDeadline := false
	var pollDeadlineRemaining time.Duration
	agent.fetchFn = func(ctx context.Context, _ string) ([]core.AgentSessionInfo, error) {
		deadline, ok := ctx.Deadline()
		sawPollDeadline = ok
		pollDeadlineRemaining = time.Until(deadline)
		return nil, ctx.Err()
	}
	if _, _, _, err := h.discoveryFingerprint(pollCtx, "codex", agent); err == nil || !sawPollDeadline || pollDeadlineRemaining <= 0 || pollDeadlineRemaining > catalogRequestTimeout {
		t.Fatalf("poller cancellation/deadline did not reach native fetch: deadline=%v remaining=%s err=%v", sawPollDeadline, pollDeadlineRemaining, err)
	}
	if base.ListSessionsCallCount() != 0 {
		t.Fatal("canceled native paths called disk-scan ListSessions")
	}
}
