package dshweb

// Official dsh host-command catalog (commands/list + commands/execute, Typert
// remotes on the commands registry). CordCode only bridges: request/response
// shapes mirror the official rc.2 wire (gateway single `args` object; list
// business value is a BARE array of descriptors) and are decoded into an
// intermediate wire type before mapping to core.SessionCommand — the official
// descriptor nests hint under `input` and omits `input` entirely for
// compact/export, so unmarshaling official JSON straight into core types is
// forbidden (silent field loss).
//
// Unknown commands: the official registry returns undefined and logs NOTHING
// (admission misses never enter a handler), so execute maps
// "commandId and result.kind both empty" to a failure.

import (
	"context"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	commandsListMethod    = "commands/list"
	commandsExecuteMethod = "commands/execute"
)

type commandsListRequest struct {
	Args commandsListArgs `json:"args"`
}

type commandsListArgs struct {
	AgentID string `json:"agentId"`
}

// commandDescriptorWire mirrors the official CommandDescriptor {name,
// description, input?{hint, images?}} (rc.2 interaction/commands/src/index.ts
// normalizeDefinition). Input is a pointer: compact/export have no input key.
type commandDescriptorWire struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Input       *commandInputWire `json:"input,omitempty"`
}

type commandInputWire struct {
	Hint   string `json:"hint"`
	Images bool   `json:"images,omitempty"`
}

type commandsExecuteRequest struct {
	Args commandsExecuteArgs `json:"args"`
}

// commandsExecuteArgs.images MUST always serialize as an array. The deployed
// seat gateway (live-probed 2026-09-05) enforces the execute descriptor
// strictly: omitting images fails with `args fields do not match the
// descriptor: missing "images"` (rc.2 fixtures tolerate omission; the seat
// does not), and images:null fails boundary validation. An empty array is the
// official no-attachment invocation ("empty for a plain invocation", rc.2
// interaction/commands execute docstring), so `images: []` is the one shape
// both gateway generations accept. Phase 1 has no image-command surface, so
// the slice is always empty; the element type mirrors EncodedImageAttachment
// for the day it is not.
type encodedImageAttachment struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

type commandsExecuteArgs struct {
	AgentID string                   `json:"agentId"`
	Line    string                   `json:"line"`
	Images  []encodedImageAttachment `json:"images"`
}

type commandsExecuteValue struct {
	CommandID string `json:"commandId"`
	Result    struct {
		Kind string `json:"kind"`
		Text string `json:"text,omitempty"`
	} `json:"result"`
}

var _ core.SessionCommandCatalog = (*Agent)(nil)

// ListSessionCommands fetches the official name-sorted bare descriptor array
// and maps it to core.SessionCommand. input.images is dropped by design
// (phase 1 has no image-command surface); empty names are dropped; skills are
// never merged in; /model is never injected (it is not a host command).
func (a *Agent) ListSessionCommands(ctx context.Context, sessionID string) ([]core.SessionCommand, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("dsh-web: list session commands: empty session id")
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var descriptors []commandDescriptorWire
	if err := client.Call(ctx, commandsListMethod, commandsListRequest{
		Args: commandsListArgs{AgentID: sessionID},
	}, &descriptors); err != nil {
		return nil, err
	}
	commands := make([]core.SessionCommand, 0, len(descriptors))
	for _, d := range descriptors {
		if d.Name == "" {
			continue
		}
		cmd := core.SessionCommand{Name: d.Name, Description: d.Description}
		if d.Input != nil {
			cmd.Hint = d.Input.Hint
		}
		commands = append(commands, cmd)
	}
	return commands, nil
}

// ExecuteSessionCommand runs one official slash line host-side. The command
// is never a user message (contrast session.prompt, which always creates one).
// Failures: official result.kind=="error" (message carries official
// result.text), and the official undefined-miss (commandId and result.kind
// both empty → "command not matched"). Success carries the official settle
// (commandId + result.kind + result.text) so clients can show real feedback.
func (a *Agent) ExecuteSessionCommand(ctx context.Context, sessionID, line string) (core.SessionCommandResult, error) {
	var zero core.SessionCommandResult
	if sessionID == "" {
		return zero, fmt.Errorf("dsh-web: execute session command: empty session id")
	}
	if line == "" || !strings.HasPrefix(line, "/") || strings.ContainsAny(line, "\n\r") {
		return zero, fmt.Errorf("dsh-web: execute session command: line must be a single '/'-prefixed line")
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return zero, err
	}
	var out commandsExecuteValue
	if err := client.Call(ctx, commandsExecuteMethod, commandsExecuteRequest{
		Args: commandsExecuteArgs{AgentID: sessionID, Line: line, Images: []encodedImageAttachment{}},
	}, &out); err != nil {
		return zero, err
	}
	if strings.EqualFold(out.Result.Kind, "error") {
		if out.Result.Text != "" {
			return zero, fmt.Errorf("dsh-web: %s: %s", line, out.Result.Text)
		}
		return zero, fmt.Errorf("dsh-web: %s: command error", line)
	}
	if out.CommandID == "" && out.Result.Kind == "" {
		return zero, fmt.Errorf("dsh-web: %s: command not matched", line)
	}
	return core.SessionCommandResult{
		CommandID:  out.CommandID,
		ResultKind: out.Result.Kind,
		ResultText: out.Result.Text,
	}, nil
}
