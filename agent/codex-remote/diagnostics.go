package codexremote

// InstanceStatus is a read-only mirror. Phase 1 identity has no enrollment
// yet, so the backend is unavailable with a truthful not_configured detail.
func (a *Agent) InstanceStatus() (bool, string) {
	return false, ErrNotConfigured.Error()
}
