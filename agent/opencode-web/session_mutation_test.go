package opencodeweb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Official shapes (live-pinned on the real 1.18.18 sandbox serve, 2026-08-20):
//
//	DELETE /session/{id}            → 200 `true`; GET afterwards 404
//	PATCH  /session/{id}            → 200 Session.Info with time.archived echoed
//	       body {"time":{"archived": <epoch ms number>}}
//
// v2 carries the same routes under /api with time.archived explicitly in the
// SDK SessionUpdateData body type.

func TestDeleteSessionOfficialShapeAndSuccess(t *testing.T) {
	agent, serve := newDataAgent(t, map[string]string{}, "/tmp/proj")
	serve.methodResponses = map[string]string{
		"DELETE /session/ses_del1": `true`,
	}
	if err := agent.DeleteSession(context.Background(), "ses_del1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	reqs := serve.requestsFor("/session/ses_del1")
	if len(reqs) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != "DELETE" || !req.Authed {
		t.Fatalf("request must be an authed DELETE, got %+v", req)
	}
	if req.Body != "" {
		t.Fatalf("SessionDeleteData declares body?: never — got body %q", req.Body)
	}
	if req.Directory != "/tmp/proj" {
		t.Fatalf("1.18 SDK carries the directory query on delete; header recorded %q", req.Directory)
	}
}

func TestDeleteSessionPropagatesHTTPError(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{}, "/tmp/proj")
	err := agent.DeleteSession(context.Background(), "ses_missing")
	if err == nil {
		t.Fatal("404 must surface as an error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("error must stay diagnosable with the HTTP code: %v", err)
	}
}

func TestArchiveSessionOfficialShapeAndMapping(t *testing.T) {
	agent, serve := newDataAgent(t, map[string]string{}, "/tmp/proj")
	archivedMs := float64(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC).UnixMilli())
	patchBody, _ := json.Marshal(map[string]any{
		"id":        "ses_arc1",
		"title":     "讲个猴哥语录",
		"directory": "/tmp/proj",
		"time": map[string]any{
			"created":  archivedMs - 1000,
			"updated":  archivedMs - 500,
			"archived": archivedMs,
		},
		"model": map[string]any{"id": "glm-4.7", "providerID": "zhipuai-coding-plan"},
	})
	serve.methodResponses = map[string]string{
		"PATCH /session/ses_arc1": string(patchBody),
	}
	archivedAt := time.UnixMilli(int64(archivedMs)).UTC()
	info, err := agent.ArchiveSession(context.Background(), "ses_arc1", archivedAt)
	if err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if info.ID != "ses_arc1" || info.Summary != "讲个猴哥语录" || info.Directory != "/tmp/proj" {
		t.Fatalf("mapped info %+v", info)
	}
	if !info.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("ArchivedAt = %v, want %v", info.ArchivedAt, archivedAt)
	}
	if info.ModelID != "glm-4.7" || info.ProviderID != "zhipuai-coding-plan" {
		t.Fatalf("model mapping %+v", info)
	}
	reqs := serve.requestsFor("/session/ses_arc1")
	if len(reqs) != 1 || reqs[0].Method != "PATCH" || !reqs[0].Authed {
		t.Fatalf("request must be an authed PATCH, got %+v", reqs)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatalf("body decode: %v (%s)", err, reqs[0].Body)
	}
	timeObj, ok := body["time"].(map[string]any)
	if !ok {
		t.Fatalf("official update body nests the timestamp under time: %s", reqs[0].Body)
	}
	if got, ok := timeObj["archived"].(float64); !ok || int64(got) != int64(archivedMs) {
		t.Fatalf("time.archived = %v, want epoch-ms %v", timeObj["archived"], archivedMs)
	}
	if _, hasTitle := body["title"]; hasTitle {
		t.Fatalf("archive must not touch title: %s", reqs[0].Body)
	}
}

func TestArchiveSessionPropagatesHTTPError(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{}, "/tmp/proj")
	_, err := agent.ArchiveSession(context.Background(), "ses_missing", time.Now())
	if err == nil {
		t.Fatal("404 must surface as an error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("error must stay diagnosable with the HTTP code: %v", err)
	}
}

// Live 1.18 caveat: the default GET /session list keeps returning archived
// rows — the mapper must surface time.archived so clients can hide them.
func TestArchivedAtMappingKeptForByIDReads(t *testing.T) {
	// OD-1 (C2): archived rows are hidden from DEFAULT enumeration, but the
	// by-ID read keeps the row and its ArchivedAt mapping (hide != delete).
	archived := float64(time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli())
	payload, _ := json.Marshal([]map[string]any{{
		"id":        "ses_archived",
		"title":     "已归档",
		"directory": "/tmp/proj",
		"time":      map[string]any{"created": archived - 2000, "updated": archived - 1000, "archived": archived},
	}})
	byID, _ := json.Marshal(map[string]any{
		"id":        "ses_archived",
		"title":     "已归档",
		"directory": "/tmp/proj",
		"time":      map[string]any{"created": archived - 2000, "updated": archived - 1000, "archived": archived},
	})
	agent, _ := newDataAgent(t, map[string]string{
		"/session":              string(payload),
		"/session/ses_archived": string(byID),
	}, "/tmp/proj")
	scoped, err := agent.ListSessionsInDirectory(context.Background(), "/tmp/proj")
	if err != nil {
		t.Fatalf("scoped listing: %v", err)
	}
	if len(scoped) != 0 {
		t.Fatalf("archived row must be hidden from the default scoped enumeration, got %+v", scoped)
	}
	info, err := agent.FetchSessionInfo(context.Background(), "ses_archived")
	if err != nil {
		t.Fatalf("FetchSessionInfo must keep the archived row: %v", err)
	}
	want := time.UnixMilli(int64(archived)).UTC()
	if !info.ArchivedAt.Equal(want) {
		t.Fatalf("ArchivedAt = %v, want %v", info.ArchivedAt, want)
	}
	// Interface conformance for the bridge capability gates.
	if _, ok := interface{}(agent).(core.SessionDeleter); !ok {
		t.Fatal("Agent must implement core.SessionDeleter")
	}
	if _, ok := interface{}(agent).(core.SessionArchiver); !ok {
		t.Fatal("Agent must implement core.SessionArchiver")
	}
}

func TestFetchSessionInfoOfficialShapeAndMapping(t *testing.T) {
	archivedMs := float64(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC).UnixMilli())
	row, _ := json.Marshal(map[string]any{
		"id":        "ses_fetch1",
		"title":     "单取",
		"directory": "/tmp/proj",
		"time":      map[string]any{"created": archivedMs - 2000, "updated": archivedMs - 1000, "archived": archivedMs},
		"model":     map[string]any{"id": "glm-4.7", "providerID": "zhipuai-coding-plan"},
	})
	agent, serve := newDataAgent(t, map[string]string{"/session/ses_fetch1": string(row)}, "/tmp/proj")
	info, err := agent.FetchSessionInfo(context.Background(), "ses_fetch1")
	if err != nil {
		t.Fatalf("FetchSessionInfo: %v", err)
	}
	if info.ID != "ses_fetch1" || info.Summary != "单取" || info.Directory != "/tmp/proj" {
		t.Fatalf("mapped info %+v", info)
	}
	if !info.ArchivedAt.Equal(time.UnixMilli(int64(archivedMs)).UTC()) {
		t.Fatalf("ArchivedAt = %v, want %v", info.ArchivedAt, time.UnixMilli(int64(archivedMs)).UTC())
	}
	if info.ModelID != "glm-4.7" || info.ProviderID != "zhipuai-coding-plan" {
		t.Fatalf("model mapping %+v", info)
	}
	reqs := serve.requestsFor("/session/ses_fetch1")
	if len(reqs) == 0 || reqs[0].Method != "GET" || !reqs[0].Authed {
		t.Fatalf("expected an authed GET by id, got %+v", reqs)
	}
}

func TestFetchSessionInfoPropagatesHTTPError(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{}, "/tmp/proj")
	_, err := agent.FetchSessionInfo(context.Background(), "ses_missing")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected diagnosable 404 error, got %v", err)
	}
}
