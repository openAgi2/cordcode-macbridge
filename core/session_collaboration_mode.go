package core

import "context"

// SessionCollaborationMode is the backend-neutral, authoritative effective
// collaboration state for one thread. It is intentionally separate from
// permission mode and the Grok/dsh session-mode surfaces.
type SessionCollaborationMode struct {
	Mode            string  `json:"mode"`
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoningEffort,omitempty"`
}

// SessionCollaborationModePreset is one official preset returned by the
// backend catalog. Name is the stable selection identity. Empty Mode means
// the preset mask preserves the thread's current mode.
type SessionCollaborationModePreset struct {
	Name            string  `json:"name"`
	Mode            string  `json:"mode,omitempty"`
	Model           *string `json:"model,omitempty"`
	ReasoningEffort *string `json:"reasoningEffort,omitempty"`
}

// SessionCollaborationModeCatalog combines official presets with the current
// authoritative thread state. Implementations must fail rather than invent a
// Current value when the backend has provided no cold/read notification source.
type SessionCollaborationModeCatalog struct {
	Presets []SessionCollaborationModePreset `json:"presets"`
	Current SessionCollaborationMode         `json:"current"`
}

// SessionCollaborationModeController is the typed Plan/Default surface.
// Update accepts an official preset name and only acknowledges submission;
// the subsequent authoritative state event owns convergence.
type SessionCollaborationModeController interface {
	ListSessionCollaborationModes(ctx context.Context, sessionID string) (SessionCollaborationModeCatalog, error)
	CurrentSessionCollaborationMode(sessionID string) (SessionCollaborationMode, bool)
	UpdateSessionCollaborationMode(ctx context.Context, sessionID, presetName string) error
}
