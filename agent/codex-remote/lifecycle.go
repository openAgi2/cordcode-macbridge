package codexremote

// EnrollmentState is the Remote Control controller lifecycle. Phase 1 identity
// starts absent; auth/pair/environment/WSS land in the transport-rpc unit.
type EnrollmentState string

const (
	EnrollmentAbsent EnrollmentState = "absent"
	EnrollmentReady  EnrollmentState = "ready"
)

func (a *Agent) enrollmentState() EnrollmentState {
	return EnrollmentAbsent
}
