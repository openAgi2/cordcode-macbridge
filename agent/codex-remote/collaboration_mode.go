package codexremote

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type collaborationModeMask struct {
	Name            string
	Mode            string
	Model           *string
	ReasoningEffort *string
}

func (a *Agent) CurrentSessionCollaborationMode(sessionID string) (core.SessionCollaborationMode, bool) {
	a.mu.Lock()
	codec := a.codec
	a.mu.Unlock()
	if codec == nil {
		return core.SessionCollaborationMode{}, false
	}
	return codec.CurrentCollaborationMode(strings.TrimSpace(sessionID))
}

// GetSessionCollaborationMode reads the complete authoritative thread settings
// snapshot from the app-server. Unlike the update ACK, this is a readback and
// can seed a fresh controller epoch without waiting for a future mutation.
func (a *Agent) GetSessionCollaborationMode(ctx context.Context, sessionID string) (core.SessionCollaborationMode, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return core.SessionCollaborationMode{}, fmt.Errorf("codex-remote: collaboration mode get: empty session id")
	}
	cl, codec, err := a.collaborationClient()
	if err != nil {
		return core.SessionCollaborationMode{}, err
	}
	return a.getSessionCollaborationModeOn(ctx, cl, codec, sessionID)
}

func (a *Agent) getSessionCollaborationModeOn(ctx context.Context, cl *Client, codec *LiveCodec, sessionID string) (core.SessionCollaborationMode, error) {
	baseVersion := codec.CollaborationVersion(sessionID)
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/settings/get", map[string]any{"threadId": sessionID})
	if err != nil {
		return core.SessionCollaborationMode{}, err
	}
	if rpcErr != nil {
		return core.SessionCollaborationMode{}, rpcErr
	}
	state, err := decodeCollaborationModeGetResponse(raw)
	if err != nil {
		return core.SessionCollaborationMode{}, fmt.Errorf("codex-remote: thread/settings/get decode: %w", err)
	}
	current, currentEpoch := a.applyCollaborationResponse(cl, codec, sessionID, state, baseVersion)
	if !currentEpoch {
		return core.SessionCollaborationMode{}, fmt.Errorf("codex-remote: connection changed during thread/settings/get")
	}
	return current, nil
}

func (a *Agent) refreshSessionCollaborationAfterAttach(ctx context.Context, cl *Client, threadID string) error {
	if !a.CollaborationModeReady() {
		return nil
	}
	a.mu.Lock()
	codec := a.codec
	current := a.client == cl
	a.mu.Unlock()
	if !current || codec == nil {
		return ErrNotConfigured
	}
	_, err := a.getSessionCollaborationModeOn(ctx, cl, codec, threadID)
	if rpcErr, ok := err.(*RPCError); ok && isMissingThreadSettingsGetMethod(rpcErr) {
		// Desktop 26.903.61454 embeds Codex 0.153.4, which predates the
		// authoritative read method. Keep the attachment alive, but do not
		// invent a collaboration baseline: a later complete settings
		// notification may populate the codec, while list/update remain
		// fail-closed until that happens.
		return nil
	}
	return err
}

func isMissingThreadSettingsGetMethod(err *RPCError) bool {
	if err == nil {
		return false
	}
	if err.Code == -32601 {
		return true
	}
	// The signed Codex 0.153.4 protocol enum reports an unregistered
	// experimental method as invalid request (-32600), naming the exact
	// unknown variant. Only that observed missing-method shape is tolerated.
	return err.Code == -32600 &&
		strings.Contains(err.Message, "unknown variant `thread/settings/get`")
}

func (a *Agent) collaborationClient() (*Client, *LiveCodec, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.codec == nil {
		return nil, nil, ErrNotConfigured
	}
	return a.client, a.codec, nil
}

func (a *Agent) applyCollaborationResponse(cl *Client, codec *LiveCodec, threadID string, state core.SessionCollaborationMode, baseVersion uint64) (core.SessionCollaborationMode, bool) {
	a.mu.Lock()
	if a.client != cl || a.codec != codec {
		a.mu.Unlock()
		return core.SessionCollaborationMode{}, false
	}
	event, applied := codec.applyCollaborationMode(threadID, state, &baseVersion)
	current, exists := codec.CurrentCollaborationMode(threadID)
	a.mu.Unlock()
	if !exists {
		return core.SessionCollaborationMode{}, false
	}
	if applied {
		a.dispatchForClient(cl, event)
	}
	return current, true
}

func decodeCollaborationModeGetResponse(raw json.RawMessage) (core.SessionCollaborationMode, error) {
	var response struct {
		ThreadSettings json.RawMessage `json:"threadSettings"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return core.SessionCollaborationMode{}, err
	}
	if len(response.ThreadSettings) == 0 {
		return core.SessionCollaborationMode{}, fmt.Errorf("response omitted threadSettings")
	}
	return decodeCollaborationModeFromThreadSettings(response.ThreadSettings)
}

func decodeCollaborationModeFromThreadSettings(raw json.RawMessage) (core.SessionCollaborationMode, error) {
	var settings struct {
		CollaborationMode struct {
			Mode     string `json:"mode"`
			Settings struct {
				Model           string  `json:"model"`
				ReasoningEffort *string `json:"reasoning_effort"`
			} `json:"settings"`
		} `json:"collaborationMode"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return core.SessionCollaborationMode{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(settings.CollaborationMode.Mode))
	model := strings.TrimSpace(settings.CollaborationMode.Settings.Model)
	if model == "" || (mode != "plan" && mode != "default") {
		return core.SessionCollaborationMode{}, fmt.Errorf("invalid collaboration mode or model")
	}
	return core.SessionCollaborationMode{
		Mode:            mode,
		Model:           model,
		ReasoningEffort: cloneStringPointer(settings.CollaborationMode.Settings.ReasoningEffort),
	}, nil
}

func (a *Agent) ListSessionCollaborationModes(ctx context.Context, sessionID string) (core.SessionCollaborationModeCatalog, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return core.SessionCollaborationModeCatalog{}, fmt.Errorf("codex-remote: collaboration mode: empty session id")
	}
	presets, err := a.fetchCollaborationModePresets(ctx)
	if err != nil {
		return core.SessionCollaborationModeCatalog{}, err
	}
	current, ok := a.CurrentSessionCollaborationMode(sessionID)
	if !ok {
		return core.SessionCollaborationModeCatalog{}, fmt.Errorf("codex-remote: authoritative collaboration settings are unavailable for thread %s", sessionID)
	}
	out := core.SessionCollaborationModeCatalog{Current: current, Presets: make([]core.SessionCollaborationModePreset, 0, len(presets))}
	for _, preset := range presets {
		out.Presets = append(out.Presets, core.SessionCollaborationModePreset{
			Name: preset.Name, Mode: preset.Mode, Model: cloneStringPointer(preset.Model), ReasoningEffort: cloneStringPointer(preset.ReasoningEffort),
		})
	}
	return out, nil
}

func (a *Agent) UpdateSessionCollaborationMode(ctx context.Context, sessionID, presetName string) error {
	sessionID = strings.TrimSpace(sessionID)
	presetName = strings.TrimSpace(presetName)
	if sessionID == "" || presetName == "" {
		return fmt.Errorf("codex-remote: collaboration mode: session id and preset are required")
	}
	payload, err := a.collaborationModePayload(ctx, sessionID, func(p collaborationModeMask) bool { return p.Name == presetName })
	if err != nil {
		return err
	}
	return a.submitThreadCollaborationMode(ctx, sessionID, payload)
}

func (a *Agent) collaborationModePayloadForKind(ctx context.Context, sessionID, modeKind string) (map[string]any, error) {
	modeKind = strings.ToLower(strings.TrimSpace(modeKind))
	return a.collaborationModePayload(ctx, sessionID, func(p collaborationModeMask) bool { return p.Mode == modeKind })
}

func (a *Agent) collaborationModePayload(ctx context.Context, sessionID string, match func(collaborationModeMask) bool) (map[string]any, error) {
	current, ok := a.CurrentSessionCollaborationMode(sessionID)
	if !ok {
		return nil, fmt.Errorf("codex-remote: authoritative collaboration settings are unavailable for thread %s", sessionID)
	}
	presets, err := a.fetchCollaborationModePresets(ctx)
	if err != nil {
		return nil, err
	}
	var selected *collaborationModeMask
	for i := range presets {
		if match(presets[i]) {
			selected = &presets[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("codex-remote: requested collaboration preset is not available")
	}
	mode := current.Mode
	if selected.Mode != "" {
		mode = selected.Mode
	}
	model := current.Model
	if selected.Model != nil {
		model = *selected.Model
	}
	effort := cloneStringPointer(current.ReasoningEffort)
	if selected.ReasoningEffort != nil {
		effort = cloneStringPointer(selected.ReasoningEffort)
	}
	if mode == "" || model == "" {
		return nil, fmt.Errorf("codex-remote: official collaboration settings are incomplete")
	}
	return map[string]any{
		"mode": mode,
		"settings": map[string]any{
			"model":                  model,
			"reasoning_effort":       effort,
			"developer_instructions": nil,
		},
	}, nil
}

func (a *Agent) fetchCollaborationModePresets(ctx context.Context) ([]collaborationModeMask, error) {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return nil, ErrNotConfigured
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "collaborationMode/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	if rpcErr != nil {
		return nil, rpcErr
	}
	var response struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("codex-remote: collaborationMode/list decode: %w", err)
	}
	if response.Data == nil {
		return nil, fmt.Errorf("codex-remote: collaborationMode/list omitted data")
	}
	presets := make([]collaborationModeMask, 0, len(response.Data))
	seen := map[string]struct{}{}
	for _, entry := range response.Data {
		var wire struct {
			Name            string  `json:"name"`
			Mode            *string `json:"mode"`
			Model           *string `json:"model"`
			ReasoningEffort *string `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(entry, &wire); err != nil {
			return nil, fmt.Errorf("codex-remote: collaborationMode/list preset decode: %w", err)
		}
		wire.Name = strings.TrimSpace(wire.Name)
		if wire.Name == "" {
			return nil, fmt.Errorf("codex-remote: collaborationMode/list returned an unnamed preset")
		}
		if _, duplicate := seen[wire.Name]; duplicate {
			return nil, fmt.Errorf("codex-remote: collaborationMode/list returned duplicate preset %q", wire.Name)
		}
		seen[wire.Name] = struct{}{}
		mode := ""
		if wire.Mode != nil {
			mode = strings.ToLower(strings.TrimSpace(*wire.Mode))
			if mode != "plan" && mode != "default" {
				return nil, fmt.Errorf("codex-remote: collaborationMode/list returned unsupported mode %q", mode)
			}
		}
		presets = append(presets, collaborationModeMask{Name: wire.Name, Mode: mode, Model: cloneStringPointer(wire.Model), ReasoningEffort: cloneStringPointer(wire.ReasoningEffort)})
	}
	if len(presets) == 0 {
		return nil, fmt.Errorf("codex-remote: collaborationMode/list returned no presets")
	}
	return presets, nil
}

func (a *Agent) submitThreadCollaborationMode(ctx context.Context, threadID string, mode map[string]any) error {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return ErrNotConfigured
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/settings/update", map[string]any{"threadId": threadID, "collaborationMode": mode})
	if err != nil {
		return err
	}
	if rpcErr != nil {
		return rpcErr
	}
	return decodeStrictEmptyObject(raw, "thread/settings/update")
}

func decodeStrictEmptyObject(raw json.RawMessage, method string) error {
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil || response == nil {
		if err == nil {
			err = fmt.Errorf("response is not an object")
		}
		return fmt.Errorf("codex-remote: %s decode: %w", method, err)
	}
	if len(response) != 0 {
		return fmt.Errorf("codex-remote: %s returned unexpected fields", method)
	}
	return nil
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

var _ core.SessionCollaborationModeController = (*Agent)(nil)
