package codexremote

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestOfficialModelCatalogAndPerTurnSelection(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_models", "env_desktop", "stream_models")
	defer stream.Close()
	var turnParams map[string]any
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		switch method {
		case "model/list":
			var request map[string]any
			_ = json.Unmarshal(params, &request)
			if request["cursor"] == "page-2" {
				return map[string]any{"data": []any{map[string]any{
					"id": "gpt-5.6-luna", "displayName": "Luna", "description": "Fast",
					"supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "low"}},
					"defaultReasoningEffort":    "low",
				}}}, nil
			}
			return map[string]any{
				"data": []any{
					map[string]any{
						"id": "gpt-5.6-sol", "displayName": "Sol", "description": "Frontier", "isDefault": true,
						"supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "medium"}, map[string]any{"reasoningEffort": "high"}},
						"defaultReasoningEffort":    "medium",
					},
					map[string]any{"id": "hidden", "hidden": true},
				},
				"nextCursor": "page-2",
			}, nil
		case "thread/resume":
			return map[string]any{
				"thread": map[string]any{"id": "thread_models"},
				"model":  "gpt-5.6-sol", "modelProvider": "openai", "reasoningEffort": "medium",
			}, nil
		case "turn/start":
			_ = json.Unmarshal(params, &turnParams)
			return map[string]any{"turn": map[string]any{"id": "turn_models", "status": "inProgress"}}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)

	models := agent.AvailableModels(context.Background())
	if len(models) != 2 || models[0].Name != "gpt-5.6-sol" || models[1].Name != "gpt-5.6-luna" {
		t.Fatalf("models = %+v", models)
	}
	if agent.GetModel() != "gpt-5.6-sol" {
		t.Fatalf("default model = %q", agent.GetModel())
	}
	efforts, defaultEffort, ok := agent.EffortsForModel(context.Background(), "gpt-5.6-sol")
	if !ok || len(efforts) != 2 || defaultEffort != "medium" {
		t.Fatalf("efforts=%v default=%q ok=%v", efforts, defaultEffort, ok)
	}

	sess, err := agent.StartSession(context.Background(), "thread_models")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.(core.PromptOptionsSender).SendWithOptions("hello", nil, nil, core.PromptOptions{
		ProviderID: "openai", ModelID: "gpt-5.6-luna", ReasoningEffort: "low",
	}); err != nil {
		t.Fatal(err)
	}
	if turnParams["model"] != "gpt-5.6-luna" || turnParams["effort"] != "low" {
		t.Fatalf("turn/start params = %#v", turnParams)
	}
	selection, ok := agent.GetSessionModelSelection(context.Background(), "thread_models")
	if !ok || selection.Provider != "openai" || selection.Model != "gpt-5.6-sol" || selection.ReasoningEffort != "medium" {
		t.Fatalf("session selection = %+v ok=%v", selection, ok)
	}
}

func TestModelListRejectsRepeatedCursor(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_models", "env_desktop", "stream_models")
	defer stream.Close()
	startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
		if method == "model/list" {
			return map[string]any{"data": []any{}, "nextCursor": "same"}, nil
		}
		return nil, &RPCError{Code: -32601, Message: method}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	if _, err := listRemoteModels(context.Background(), cl); err == nil {
		t.Fatal("repeated model/list cursor must fail instead of looping")
	}
}
