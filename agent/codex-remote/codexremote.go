// Package codexremote is CordCode's independent Remote Control backend:
// ChatGPT Desktop private app-server → OpenAI Remote Control → MacBridge
// controller → ordinary app-server JSON-RPC (design
// docs/2026-08-26-codex-remote-backend-implementation-plan.md).
//
// Isolation:
//   - does not import the retired Codex backend or the shared-daemon Codex Web backend;
//   - does not use JSONL / rollout / file-relay as a live substitute;
//   - does not advertise cursor reconnect or official iOS controller
//     coexistence until those have product-path evidence.
package codexremote

import "github.com/openAgi2/cordcode-macbridge/core"

// BackendID is the go-bridge driver id (drivers flag / hello_ack backends[]).
const BackendID = "codex-remote"

// WireKind is the iOS backend kind. Independent from "codex" and "codex-web".
const WireKind = "codex-remote"

// DisplayName is the product label for this backend.
const DisplayName = "Codex Desktop"

func init() {
	core.RegisterAgent(BackendID, NewAgentFactory)
}

// NewAgentFactory matches core.RegisterAgent.
func NewAgentFactory(opts map[string]any) (core.Agent, error) {
	return New(opts), nil
}

var _ core.Agent = (*Agent)(nil)
