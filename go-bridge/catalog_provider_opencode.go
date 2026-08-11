package gobridge

import "context"

// catalog_provider_opencode.go是 SessionCatalogProvider 的 OpenCode 适配器（设计 §4.1 /
// §5.3，Phase 1 chunk 1A）。它包装现有 OpenCodeProxy.listSessions（HTTP /session，
// array-only，Phase 0 冻结），把上游 raw session 规范化成 NormalizedSession，并派生
// 有序 fingerprint。
//
// 这是纯 seam：FetchPage0 在 Phase 1A **不**接入 ocHandleListSessions（接入属 1C，
// 并由 catalog_cursor_epoch_v2 capability 门控）。现有 live list 路径
// （ocProxy.listSessions → mapSession → enrich/overlay/sort → paginateSessionList）
// 行为不变；本文件不触碰它。

// openCodeSessionLister 是 openCodeCatalogProvider 与具体 OpenCodeProxy 之间的 seam。
// *OpenCodeProxy 满足该接口。定义为接口（而非具体指针）使 provider 可在不启动 live
// `opencode serve` 的情况下用 Phase 0 fixture 单测；Phase 1D 的 singleflight/timeout/
// cancel 装饰器也会以同一接口包裹 lister。
type openCodeSessionLister interface {
	listSessions(OpenCodeSessionListOptions) (OpenCodeSessionListResult, error)
}

// openCodeCatalogProvider 把 OpenCode /session 适配成 SessionCatalogProvider。
type openCodeCatalogProvider struct {
	lister openCodeSessionLister
}

// newOpenCodeCatalogProvider 用现有 OpenCodeProxy 构造一个 OpenCode catalog provider。
func newOpenCodeCatalogProvider(proxy *OpenCodeProxy) *openCodeCatalogProvider {
	return &openCodeCatalogProvider{lister: proxy}
}

// FetchPage0 对 OpenCode /session 做一次有界全量读取并规范化。上游 fetch 预算固定为
// openCodeSessionFetchLimit=100（OpenCode /session 硬上限，Phase 0 冻结；>100 → HTTP 500）。
// 不转发 client cursor 上游（/session 无上游 cursor）；不按 client limit 切片（切片属
// Phase 1B snapshot cache）。
//
// ctx 契约：1A 阶段 lister（OpenCodeProxy.listSessions）尚未接受 ctx，故本方法暂不在
// 上游调用中途 honor ctx；Phase 1D 会用 ctx+timeout 装饰器包裹 lister 真正支持取消。
func (p *openCodeCatalogProvider) FetchPage0(_ context.Context, q CatalogQuery) (CatalogPage0, error) {
	page, err := p.lister.listSessions(OpenCodeSessionListOptions{
		Directory: q.Directory,
		Limit:     openCodeSessionFetchLimit,
		Roots:     q.RootsOnly,
	})
	if err != nil {
		return CatalogPage0{}, err
	}
	sessions := make([]NormalizedSession, 0, len(page.Sessions))
	for _, raw := range page.Sessions {
		sessions = append(sessions, normalizeOpenCodeSession(raw))
	}
	// OpenCode /session 上游按 time.updated desc 返回（Phase 0 contract test 锁定）。
	// §4.2「顺序 = backend catalog 排序」——provider 保持上游序，不本地重排；
	// fingerprintCatalog 内部按 StableID 排序使 fingerprint 与 backend 瞬时序解耦。
	return CatalogPage0{
		Sessions:    sessions,
		Fingerprint: fingerprintCatalog(sessions),
	}, nil
}

// normalizeOpenCodeSession 把 OpenCode /session 的 raw 条目（Phase 0 frozen schema：
// id/slug/projectID/directory/path/summary/cost/tokens/title/agent/model/version/time）
// 规范化成 NormalizedSession。
//
// 字段读取与 mapSession（opencode-proxy.go）的提取同源（id←id/session_id、
// projectID←projectID/projectId、parentID←parentID/parentId、time←time.{created,updated,
// archived}），但 provider 只做 catalog 投影，**不**做 mapSession 的 wire 兜底（如
// title←summary、availability/runtimeState 等展示语义）——§4.2「标题 = backend catalog
// 返回值」，OpenCode catalog 原生返回独立 title 字段（Phase 0 fixture 坐实 title 是
// 独立 string、summary 是 diff 统计 dict），不需要 summary 兜底。
func normalizeOpenCodeSession(s map[string]interface{}) NormalizedSession {
	id, _ := s["id"].(string)
	if id == "" {
		id, _ = s["session_id"].(string)
	}
	title, _ := s["title"].(string)
	directory, _ := s["directory"].(string)
	projectID, _ := s["projectID"].(string)
	if projectID == "" {
		projectID, _ = s["projectId"].(string)
	}
	parentID, _ := s["parentID"].(string)
	if parentID == "" {
		parentID, _ = s["parentId"].(string)
	}

	var created, updated, archived float64
	if tm, ok := s["time"].(map[string]interface{}); ok {
		created, _ = tm["created"].(float64)
		updated, _ = tm["updated"].(float64)
		archived, _ = tm["archived"].(float64)
	}

	return NormalizedSession{
		StableID:         id,
		BackendID:        "opencode",
		Title:            title,
		Directory:        directory,
		ProjectID:        projectID,
		ParentID:         parentID,
		CreatedAtMillis:  int64(created),
		UpdatedAtMillis:  int64(updated),
		Archived:         archived > 0,
		ArchivedAtMillis: int64(archived),
		IsRoot:           parentID == "",
		// OpenCode 上游序由 time.updated desc 决定；tie-break 用 id（§4.1 internal only）。
		OrderingKey: id,
	}
}
