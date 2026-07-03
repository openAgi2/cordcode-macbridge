package gobridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// OpenCodeProxy talks directly to the opencode HTTP API (port 64667).
// All methods mirror the old Node.js Unified-Bridge opencode-http driver.
type OpenCodeProxy struct {
	baseURL    string
	authHeader string
}

type OpenCodeSessionListOptions struct {
	Directory string
	Limit     int
	Cursor    string
	Roots     bool
}

type OpenCodeSessionListResult struct {
	Sessions   []map[string]interface{}
	NextCursor string
}

func NewOpenCodeProxy(baseURL, username, password string) *OpenCodeProxy {
	p := &OpenCodeProxy{baseURL: strings.TrimRight(baseURL, "/")}
	if username != "" && password != "" {
		p.authHeader = "Basic " + basicAuth(username, password)
	}
	return p
}

func (p *OpenCodeProxy) fetch(path string, opts ...func(*fetchOpts)) (json.RawMessage, error) {
	o := &fetchOpts{method: "GET"}
	for _, fn := range opts {
		fn(o)
	}

	url := p.baseURL + path
	var body io.Reader
	if o.body != nil {
		b, _ := json.Marshal(o.body)
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(context.Background(), o.method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.authHeader != "" {
		req.Header.Set("Authorization", p.authHeader)
	}
	if o.directory != "" {
		req.Header.Set("x-opencode-directory", o.directory)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("opencode HTTP %d: %s", resp.StatusCode, string(raw[:minInt(len(raw), 200)]))
	}
	return raw, nil
}

type fetchOpts struct {
	method    string
	body      interface{}
	directory string
}

func withMethod(m string) func(*fetchOpts) {
	return func(o *fetchOpts) { o.method = m }
}

func withBody(b interface{}) func(*fetchOpts) {
	return func(o *fetchOpts) { o.body = b }
}

func withDir(d string) func(*fetchOpts) {
	return func(o *fetchOpts) { o.directory = d }
}

// ── API methods ───────────────────────────────────────────────────────────────

func (p *OpenCodeProxy) listSessions(opts OpenCodeSessionListOptions) (OpenCodeSessionListResult, error) {
	path := "/session"
	query := url.Values{}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		query.Set("cursor", opts.Cursor)
	}
	if opts.Roots {
		query.Set("roots", "true")
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	raw, err := p.fetch(path, withDir(opts.Directory))
	if err != nil {
		return OpenCodeSessionListResult{}, err
	}
	return unwrapSessionList(raw)
}

func (p *OpenCodeProxy) getSession(sessionID, directory string) (map[string]interface{}, error) {
	raw, err := p.fetch("/session/"+sessionID, withDir(directory))
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (p *OpenCodeProxy) createSession(title, directory string) (map[string]interface{}, error) {
	body := map[string]interface{}{}
	if title != "" {
		body["title"] = title
	}
	if directory != "" {
		body["directory"] = directory
	}
	raw, err := p.fetch("/session", withMethod("POST"), withBody(body), withDir(directory))
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (p *OpenCodeProxy) deleteSession(sessionID, directory string) error {
	_, err := p.fetch("/session/"+sessionID, withMethod("DELETE"), withDir(directory))
	return err
}

func (p *OpenCodeProxy) abortGeneration(sessionID, directory string) error {
	_, err := p.fetch("/session/"+sessionID+"/abort", withMethod("POST"), withDir(directory))
	return err
}

func (p *OpenCodeProxy) getSessionMessages(sessionID, directory string) ([]map[string]interface{}, error) {
	raw, err := p.fetch("/session/"+sessionID+"/message", withDir(directory))
	if err != nil {
		return nil, err
	}
	return unwrapArray(raw)
}

func (p *OpenCodeProxy) sendMessage(sessionID string, body map[string]interface{}, directory string) error {
	_, err := p.fetch("/session/"+sessionID+"/message", withMethod("POST"), withBody(body), withDir(directory))
	return err
}

func (p *OpenCodeProxy) fetchTodos(sessionID, directory string) ([]map[string]interface{}, error) {
	raw, err := p.fetch("/session/"+sessionID+"/todo", withDir(directory))
	if err != nil {
		return nil, err
	}
	return unwrapArray(raw)
}

func (p *OpenCodeProxy) listModels(directory string) ([]map[string]interface{}, error) {
	raw, err := p.fetch("/global/config", withDir(directory))
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}

	providers, _ := data["providers"].(map[string]interface{})
	if providers == nil {
		providers, _ = data["provider"].(map[string]interface{})
	}

	var models []map[string]interface{}
	for providerID, provVal := range providers {
		provData, _ := provVal.(map[string]interface{})
		if provData == nil {
			continue
		}
		modelsMap, _ := provData["models"].(map[string]interface{})
		for modelID, modelVal := range modelsMap {
			mData, _ := modelVal.(map[string]interface{})
			if mData == nil {
				continue
			}
			models = append(models, mapModelEntry(modelID, providerID, mData))
		}
	}
	return models, nil
}

func (p *OpenCodeProxy) listAgents() ([]map[string]interface{}, error) {
	raw, err := p.fetch("/agent")
	if err != nil {
		return nil, err
	}
	arr, err := unwrapArray(raw)
	if err != nil {
		return nil, err
	}
	var result []map[string]interface{}
	for _, a := range arr {
		name, _ := a["name"].(string)
		if name == "" {
			name, _ = a["id"].(string)
		}
		mode, _ := a["mode"].(string)
		if mode == "" {
			mode = "primary"
		}
		result = append(result, map[string]interface{}{
			"name":        name,
			"mode":        mode,
			"hidden":      boolVal(a, "hidden"),
			"native":      boolVal(a, "native"),
			"description": strVal(a, "description"),
		})
	}
	return result, nil
}

func (p *OpenCodeProxy) listProjects() ([]map[string]interface{}, error) {
	raw, err := p.fetch("/project")
	if err != nil {
		return nil, err
	}
	arr, err := unwrapArray(raw)
	if err != nil {
		return nil, err
	}
	var result []map[string]interface{}
	for _, pr := range arr {
		id, _ := pr["id"].(string)
		dir, _ := pr["directory"].(string)
		if dir == "" {
			dir, _ = pr["path"].(string)
		}
		if dir == "" {
			dir, _ = pr["worktree"].(string)
		}
		name, _ := pr["name"].(string)
		if name == "" {
			name = filepath.Base(cleanOpenCodeProjectDir(dir))
		}
		if name == "" {
			name = id
		}
		if id == "" {
			id = dir
		}
		result = append(result, map[string]interface{}{
			"id":        id,
			"directory": dir,
			"name":      name,
		})
	}
	if visible := openCodeDesktopVisibleProjects(p.baseURL, result); len(visible) > 0 {
		return visible, nil
	}
	return result, nil
}

func openCodeDesktopVisibleProjects(baseURL string, projects []map[string]interface{}) []map[string]interface{} {
	visibleDirs := readOpenCodeDesktopVisibleProjectDirs(baseURL)
	if len(visibleDirs) == 0 {
		return nil
	}

	byDirectory := make(map[string]map[string]interface{}, len(projects))
	for _, project := range projects {
		dir, _ := project["directory"].(string)
		if dir == "" {
			continue
		}
		byDirectory[cleanOpenCodeProjectDir(dir)] = project
	}

	var visible []map[string]interface{}
	for _, dir := range visibleDirs {
		if project, ok := byDirectory[cleanOpenCodeProjectDir(dir)]; ok {
			visible = append(visible, project)
			continue
		}
		visible = append(visible, map[string]interface{}{
			"id":        dir,
			"directory": dir,
			"name":      filepath.Base(cleanOpenCodeProjectDir(dir)),
		})
	}
	return visible
}

func readOpenCodeDesktopVisibleProjectDirs(baseURL string) []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	path := filepath.Join(home, "Library", "Application Support", "ai.opencode.desktop", "opencode.global.dat")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var store map[string]string
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil
	}
	serverRaw := store["server"]
	if serverRaw == "" {
		return nil
	}
	var server struct {
		CurrentSidecarURL string `json:"currentSidecarUrl"`
		Projects          map[string][]struct {
			Worktree string `json:"worktree"`
			Expanded *bool  `json:"expanded"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(serverRaw), &server); err != nil {
		return nil
	}

	keys := []string{}
	if normalized := strings.TrimRight(baseURL, "/"); normalized != "" {
		keys = append(keys, normalized)
	}
	if server.CurrentSidecarURL != "" {
		keys = append(keys, strings.TrimRight(server.CurrentSidecarURL, "/"))
	}
	keys = append(keys, "local")

	seenKeys := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key == "" || seenKeys[key] {
			continue
		}
		seenKeys[key] = true
		entries := server.Projects[key]
		if len(entries) == 0 {
			continue
		}
		var dirs []string
		for _, entry := range entries {
			if entry.Worktree == "" {
				continue
			}
			dirs = append(dirs, entry.Worktree)
		}
		if len(dirs) > 0 {
			return dirs
		}
	}
	return nil
}

func cleanOpenCodeProjectDir(dir string) string {
	return strings.TrimRight(strings.TrimSpace(dir), "/")
}

func (p *OpenCodeProxy) resolvePermission(sessionID, permissionID, response, directory string) error {
	_, err := p.fetch("/session/"+sessionID+"/permissions/"+permissionID, withMethod("POST"), withBody(map[string]interface{}{
		"response": response,
	}), withDir(directory))
	return err
}

// ── Mapping helpers (match old Node.js bridge format) ─────────────────────────

func mapSession(s map[string]interface{}) map[string]interface{} {
	timeMap, _ := s["time"].(map[string]interface{})
	id, _ := s["id"].(string)
	if id == "" {
		id, _ = s["session_id"].(string)
	}
	title, _ := s["title"].(string)
	if title == "" {
		title, _ = s["summary"].(string)
	}
	if title == "" {
		title = "Session"
	}
	directory, _ := s["directory"].(string)
	if directory == "" {
		directory, _ = s["cwd"].(string)
	}
	projectID, _ := s["projectId"].(string)
	if projectID == "" {
		projectID, _ = s["projectID"].(string)
	}
	parentID, _ := s["parentId"].(string)
	if parentID == "" {
		parentID, _ = s["parentID"].(string)
	}

	var created, updated, archived float64
	if timeMap != nil {
		created, _ = timeMap["created"].(float64)
		updated, _ = timeMap["updated"].(float64)
		archived, _ = timeMap["archived"].(float64)
	}

	availability := "resumable"
	if archived > 0 {
		availability = "archived"
	}

	effectiveModelID, _ := s["effectiveModelId"].(string)
	if effectiveModelID == "" {
		effectiveModelID, _ = s["effective_model_id"].(string)
	}
	effectiveProviderID, _ := s["effectiveProviderId"].(string)
	if effectiveProviderID == "" {
		effectiveProviderID, _ = s["effective_provider_id"].(string)
		if effectiveProviderID == "" {
			effectiveProviderID, _ = s["providerID"].(string)
		}
	}

	result := map[string]interface{}{
		"id":                  id,
		"backendId":           "opencode",
		"title":               title,
		"createdAtMillis":     int64(created),
		"updatedAtMillis":     int64(updated),
		"directory":           directory,
		"projectId":           projectID,
		"parentId":            parentID,
		"availability":        availability,
		"isReadOnlyHistory":   false,
		"runtimeState":        "idle",
		"effectiveModelId":    effectiveModelID,
		"effectiveProviderId": effectiveProviderID,
	}
	if archived > 0 {
		result["archivedAtMillis"] = int64(archived)
	}
	if msgCount, ok := s["messageCount"].(float64); ok {
		result["messageCount"] = int(msgCount)
	}
	if msgCount, ok := s["message_count"].(float64); ok {
		result["messageCount"] = int(msgCount)
	}
	return result
}

func mapMessage(m map[string]interface{}) map[string]interface{} {
	info := m
	if sub, ok := m["info"].(map[string]interface{}); ok {
		info = sub
	}

	timeMap, _ := info["time"].(map[string]interface{})
	parts, _ := m["parts"].([]interface{})
	if parts == nil {
		parts, _ = info["parts"].([]interface{})
	}

	var content, thinking string
	var steps []map[string]interface{}
	var mappedParts []map[string]interface{}

	for _, partVal := range parts {
		part, _ := partVal.(map[string]interface{})
		if part == nil {
			continue
		}
		pt, _ := part["type"].(string)

		switch pt {
		case "text":
			text, _ := part["text"].(string)
			if text == "" {
				text, _ = part["initial"].(string)
			}
			if content != "" && text != "" {
				content += "\n"
			}
			content += text
			mappedParts = append(mappedParts, map[string]interface{}{
				"type":    "text",
				"content": text,
			})

		case "reasoning":
			text, _ := part["text"].(string)
			if text == "" {
				text, _ = part["initial"].(string)
			}
			if thinking != "" && text != "" {
				thinking += "\n"
			}
			thinking += text
			mappedParts = append(mappedParts, map[string]interface{}{
				"type":    "reasoning",
				"content": text,
			})

		case "tool":
			tool, _ := part["tool"].(map[string]interface{})
			if tool != nil {
				toolID, _ := tool["id"].(string)
				if toolID == "" {
					toolID, _ = tool["toolName"].(string)
				}
				toolName, _ := tool["toolName"].(string)
				if toolName == "" {
					toolName, _ = tool["name"].(string)
				}
				var state map[string]interface{}
				state, _ = tool["state"].(map[string]interface{})
				status := "completed"
				if state != nil {
					if s, ok := state["status"].(string); ok {
						status = s
					}
				}
				var output interface{}
				if state != nil {
					output = state["output"]
				}
				var durationMs interface{}
				if state != nil {
					durationMs = state["durationMs"]
				}

				step := map[string]interface{}{
					"id":                             toolID,
					"toolName":                       toolName,
					"status":                         status,
					"output":                         makeToolOutput(output),
					"duration":                       durationMs,
					"requiresPermissionConfirmation": false,
					"availablePermissionOptions":     []interface{}{},
				}
				steps = append(steps, step)
				mappedParts = append(mappedParts, map[string]interface{}{
					"type": "tool",
					"step": step,
				})
			}

		case "file":
			file := map[string]interface{}{
				"id":       part["id"],
				"mime":     part["mime"],
				"url":      part["url"],
				"filename": part["filename"],
			}
			if file["mime"] == nil {
				file["mime"] = "application/octet-stream"
			}
			mappedParts = append(mappedParts, map[string]interface{}{
				"type": "file",
				"file": file,
			})
		}
	}

	var timestamp float64
	if timeMap != nil {
		timestamp, _ = timeMap["created"].(float64)
	}
	if timestamp == 0 {
		timestamp = float64(time.Now().UnixMilli())
	}

	role, _ := info["role"].(string)
	if role == "" {
		role, _ = m["role"].(string)
	}
	msgID, _ := info["id"].(string)
	if msgID == "" {
		msgID, _ = m["id"].(string)
	}

	result := map[string]interface{}{
		"id":              msgID,
		"role":            role,
		"content":         content,
		"steps":           steps,
		"files":           []interface{}{},
		"parts":           mappedParts,
		"timestampMillis": int64(timestamp),
		"agentName":       strVal(info, "agent"),
		"modelId":         strVal(info, "modelID"),
		"providerId":      strVal(info, "providerID"),
		"modelName":       strVal(info, "modelName"),
	}
	if thinking != "" {
		result["thinking"] = thinking
	}
	return result
}

func makeToolOutput(value interface{}) map[string]interface{} {
	if m, ok := value.(map[string]interface{}); ok {
		if m["kind"] == "inline" || m["kind"] == "content_ref" {
			return m
		}
	}
	if s, ok := value.(string); ok {
		return map[string]interface{}{"kind": "inline", "text": s}
	}
	b, _ := json.Marshal(value)
	return map[string]interface{}{"kind": "inline", "text": string(b)}
}

func mapModelEntry(modelID, providerID string, mData map[string]interface{}) map[string]interface{} {
	name := strVal(mData, "name")
	if name == "" {
		name = modelID
	}
	provider := strVal(mData, "provider")
	if provider == "" {
		provider = providerID
	}

	var limit interface{}
	if lim, ok := mData["limit"].(map[string]interface{}); ok {
		if ctx, ok := lim["context"].(float64); ok {
			limit = int(ctx)
		} else {
			limit = lim["context"]
		}
	} else if ctx, ok := mData["limit"].(float64); ok {
		limit = int(ctx)
	}

	m := map[string]interface{}{
		"id":                        modelID,
		"name":                      name,
		"provider":                  provider,
		"providerId":                providerID,
		"reasoning":                 boolVal(mData, "reasoning"),
		"isDefault":                 boolVal(mData, "isDefault") || boolVal(mData, "is_default"),
		"limit":                     limit,
		"supportedReasoningEfforts": nil,
		"defaultReasoningEffort":    nil,
	}
	if efforts, ok := mData["supportedReasoningEfforts"].([]interface{}); ok {
		m["supportedReasoningEfforts"] = efforts
	} else if efforts, ok := mData["supported_reasoning_efforts"].([]interface{}); ok {
		m["supportedReasoningEfforts"] = efforts
	}
	if def, ok := mData["defaultReasoningEffort"].(string); ok && def != "" {
		m["defaultReasoningEffort"] = def
	}
	return m
}

// ── Utility ───────────────────────────────────────────────────────────────────

func strVal(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolVal(m map[string]interface{}, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func unwrapArray(raw json.RawMessage) ([]map[string]interface{}, error) {
	var arr []map[string]interface{}
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	for _, v := range generic {
		if slice, ok := v.([]interface{}); ok {
			var result []map[string]interface{}
			for _, item := range slice {
				if m, ok := item.(map[string]interface{}); ok {
					result = append(result, m)
				}
			}
			if len(result) > 0 {
				return result, nil
			}
		}
	}
	return nil, fmt.Errorf("no array found in response")
}

func unwrapSessionList(raw json.RawMessage) (OpenCodeSessionListResult, error) {
	var arr []map[string]interface{}
	if err := json.Unmarshal(raw, &arr); err == nil {
		return OpenCodeSessionListResult{Sessions: arr}, nil
	}

	var envelope struct {
		Data   []map[string]interface{} `json:"data"`
		Cursor struct {
			Next     string `json:"next"`
			Previous string `json:"previous"`
		} `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return OpenCodeSessionListResult{}, err
	}
	if envelope.Data == nil {
		return OpenCodeSessionListResult{}, fmt.Errorf("no session data found in response")
	}
	return OpenCodeSessionListResult{
		Sessions:   envelope.Data,
		NextCursor: envelope.Cursor.Next,
	}, nil
}

func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func nowMillis() int64 {
	return time.Now().UnixMilli()
}

// suppress unused import warning
var _ = context.Background
