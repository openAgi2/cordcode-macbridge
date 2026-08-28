package codexremote

// InstanceStatus is a read-only mirror. Phase 1 identity has no enrollment
// yet, so the backend is unavailable with a truthful not_configured detail.
func (a *Agent) InstanceStatus() (bool, string) {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil || cl.IsClosed() {
		return false, ErrNotConfigured.Error()
	}
	return true, "codex-remote controller protocol " + ProtocolVersion + " (environment stream bound)"
}
