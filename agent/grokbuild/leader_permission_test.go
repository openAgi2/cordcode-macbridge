package grokbuild

// Follower permission interaction tests for the leader subscriber (§24).
//
// Wire shape sources: params mirror the ACP requestPermissionParams verified
// on the OFF-mode driver rail (session.go handlePermissionRequest, acp_types),
// which is the same payload the leader relays for session/request_permission
// (upstream server.rs is_interaction_request shared set; extract_interaction_
// tool_call_id nests under toolCall). The envelope helpers
// (normalizeLeaderMethod / interactionInnerParams) are shared with the
// ask_user_question rail and locked by their own tests.

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const fixtureRequestPermission = `{"jsonrpc":"2.0","id":7,"method":"session/request_permission","params":{"sessionId":"sess-1","toolCall":{"toolCallId":"call_perm_7","title":"rm -rf build","kind":"execute","status":"pending"},"options":[{"optionId":"opt_allow","name":"Allow","kind":"allow_once"},{"optionId":"opt_reject","name":"Reject","kind":"reject_once"}]}}`

const fixtureRequestPermissionNoReject = `{"jsonrpc":"2.0","id":8,"method":"session/request_permission","params":{"sessionId":"sess-1","toolCall":{"toolCallId":"call_perm_8","title":"read file","kind":"read","status":"pending"},"options":[{"optionId":"opt_allow8","name":"Allow","kind":"allow_once"}]}}`

// TestLeaderSubscriberAnswerPermissionAllow: an iOS allow answers with the
// ORIGINAL numeric id and the selected allow option; the registry flushes; no
// permission_resolved emits on this path (the bridge-level optimistic close
// owns that broadcast).
func TestLeaderSubscriberAnswerPermissionAllow(t *testing.T) {
	got, _ := runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		if err := writeACPRequestRaw(c, fixtureRequestPermission); err != nil {
			return err
		}
		<-answered
		resp := readClientACPFrame(t, c)
		if fmt.Sprint(resp["id"]) != "7" {
			t.Errorf("answer id = %v, want original numeric id 7", resp["id"])
		}
		result, _ := resp["result"].(map[string]any)
		outcome, _ := result["outcome"].(map[string]any)
		if outcome == nil || outcome["outcome"] != "selected" || outcome["optionId"] != "opt_allow" {
			t.Errorf("outcome = %+v, want selected/opt_allow", result)
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	}, func(sub *LeaderSubscriber) {
		resolved, err := sub.AnswerPermission("7", core.PermissionResult{Behavior: "allow"})
		if err != nil || !resolved {
			t.Errorf("AnswerPermission = (%v, %v), want (true, nil)", resolved, err)
		}
		if sub.interactions.len() != 0 {
			t.Errorf("registry len = %d after answer, want 0", sub.interactions.len())
		}
	})
	counts := countEventTypes(got)
	if counts[core.EventPermissionRequest] != 1 {
		t.Fatalf("permission_request count = %d, want 1: %+v", counts[core.EventPermissionRequest], got)
	}
	if counts[core.EventPermissionResolved] != 0 {
		t.Fatalf("permission_resolved count = %d, want 0 (bridge-level optimistic close owns it)", counts[core.EventPermissionResolved])
	}
	for _, e := range got {
		if e.Type == core.EventPermissionRequest && (e.RequestID != "7" || e.ToolName != "rm -rf build") {
			t.Fatalf("permission event = %+v, want RequestID 7 / title rm -rf build", e)
		}
	}
}

// TestLeaderSubscriberAnswerPermissionBehaviorMapping: "always" degrades to
// the allow option (no grok equivalent), "reject" selects the reject option,
// and a deny against a request with no reject option degrades to cancelled
// (upstream Path: not an error).
func TestLeaderSubscriberAnswerPermissionBehaviorMapping(t *testing.T) {
	_, _ = runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		if err := writeACPRequestRaw(c, fixtureRequestPermission); err != nil {
			return err
		}
		if err := writeACPRequestRaw(c, fixtureRequestPermissionNoReject); err != nil {
			return err
		}
		<-answered
		frames := []map[string]any{readClientACPFrame(t, c), readClientACPFrame(t, c)}
		want := []struct {
			id      string
			outcome string
			option  any // nil where omitempty drops optionId (cancelled)
		}{
			{"7", "selected", "opt_allow"}, // always → allow degrade
			{"8", "cancelled", nil},        // reject without reject option → cancelled
		}
		for i, w := range want {
			if fmt.Sprint(frames[i]["id"]) != w.id {
				t.Errorf("frame[%d] id = %v, want %s", i, frames[i]["id"], w.id)
			}
			result, _ := frames[i]["result"].(map[string]any)
			outcome, _ := result["outcome"].(map[string]any)
			if outcome == nil || outcome["outcome"] != w.outcome {
				t.Errorf("frame[%d] outcome = %+v, want %s", i, result, w.outcome)
				continue
			}
			if outcome["optionId"] != w.option {
				t.Errorf("frame[%d] optionId = %v, want %v", i, outcome["optionId"], w.option)
			}
		}
		return nil
	}, func(sub *LeaderSubscriber) {
		if resolved, err := sub.AnswerPermission("7", core.PermissionResult{Behavior: "always"}); err != nil || !resolved {
			t.Errorf("always degrade = (%v, %v), want (true, nil)", resolved, err)
		}
		if resolved, err := sub.AnswerPermission("8", core.PermissionResult{Behavior: "reject"}); err != nil || !resolved {
			t.Errorf("reject fallback = (%v, %v), want (true, nil)", resolved, err)
		}
	})
}

// TestLeaderSubscriberPermissionResolvedBroadcast: TUI answers first — the
// official interaction_resolved broadcast must evict the entry, emit
// permission_resolved (RequestID = the wire id), and turn the iOS late answer
// into a silent no-op (the wire tombstone returns before any conn write, so
// it stays silent even after Run has torn the connection down).
func TestLeaderSubscriberPermissionResolvedBroadcast(t *testing.T) {
	got, sub := runLeaderSubscriber(t, func(c net.Conn) error {
		if err := writeACPRequestRaw(c, fixtureRequestPermission); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		return writeACPNotification(c, "_x.ai/session_notification", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "interaction_resolved", "tool_call_id": "call_perm_7"},
		})
	})
	resolvedEvents := filterEvents(got, core.EventPermissionResolved)
	if len(resolvedEvents) != 1 {
		t.Fatalf("permission_resolved events = %d, want 1: %+v", len(resolvedEvents), got)
	}
	if resolvedEvents[0].RequestID != "7" || resolvedEvents[0].Content != "resolved" {
		t.Fatalf("permission_resolved = %+v, want RequestID 7 / Content resolved", resolvedEvents[0])
	}
	if len(filterEvents(got, core.EventPermissionRequest)) != 1 {
		t.Fatalf("permission_request events = %d, want 1", len(filterEvents(got, core.EventPermissionRequest)))
	}
	if sub.interactions.len() != 0 {
		t.Fatalf("registry len = %d after resolution, want 0", sub.interactions.len())
	}
	// Late iOS answer after the leader-side resolution: silent (tombstone hit
	// before any write), resolved=true per first-answer-wins semantics.
	resolved, err := sub.AnswerPermission("7", core.PermissionResult{Behavior: "allow"})
	if err != nil || !resolved {
		t.Errorf("late AnswerPermission = (%v, %v), want (true, nil) silent", resolved, err)
	}
}

// TestLeaderSubscriberAnswerPermissionUnknownID: an unregistered (never
// surfaced) id is a real error — iOS only ever answers cards it received.
func TestLeaderSubscriberAnswerPermissionUnknownID(t *testing.T) {
	_, sub := runLeaderSubscriber(t, func(c net.Conn) error {
		if err := writeACPRequestRaw(c, fixtureRequestPermission); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	if _, err := sub.AnswerPermission("999", core.PermissionResult{Behavior: "allow"}); err == nil {
		t.Error("unknown wire id answered without error")
	}
	if _, err := sub.AnswerPermission("not-a-number", core.PermissionResult{Behavior: "allow"}); err == nil {
		t.Error("non-numeric request id answered without error")
	}
	if sub.interactions.len() != 1 {
		t.Fatalf("registry len = %d, want 1 (failed answers leave the entry pending)", sub.interactions.len())
	}
}

// TestLeaderInteractionRegistryWireIndexConsistency: the wireID index tracks
// only permission-kind entries; question entries stay toolCallID-addressed
// and eviction of one kind never disturbs the other.
func TestLeaderInteractionRegistryWireIndexConsistency(t *testing.T) {
	r := newLeaderInteractionRegistry()
	r.put(leaderInteraction{wireID: 11, toolCallID: "call_q", kind: leaderKindQuestion,
		params: askUserQuestionParams{ToolCallID: "call_q"}})
	r.put(leaderInteraction{wireID: 7, toolCallID: "call_p", kind: leaderKindPermission,
		perm: &requestPermissionParams{ToolCall: permissionToolCall{ToolCallID: "call_p"}}})

	if _, ok := r.getByWire(11); ok {
		t.Error("question wire id leaked into the permission index")
	}
	if e, ok := r.getByWire(7); !ok || e.perm == nil || e.toolCallID != "call_p" {
		t.Fatalf("getByWire(7) = %+v ok=%v", e, ok)
	}

	// Evicting the question must not touch the permission entry.
	if _, ok := r.take("call_q"); !ok {
		t.Fatal("question take failed")
	}
	if e, ok := r.getByWire(7); !ok {
		t.Fatalf("permission entry lost after question eviction: %+v", e)
	}

	// Evicting the permission tombstones BOTH axes.
	if e, ok := r.take("call_p"); !ok || e.kind != leaderKindPermission {
		t.Fatalf("permission take = %+v ok=%v", e, ok)
	}
	if r.consumedByWire(7) != true {
		t.Error("wire tombstone missing after permission take")
	}
	if r.consumed("call_p") != true {
		t.Error("tool tombstone missing after permission take")
	}

	// A replayed REQUEST revives the wire index entry.
	var p requestPermissionParams
	_ = json.Unmarshal([]byte(`{"toolCall":{"toolCallId":"call_p"}}`), &p)
	r.put(leaderInteraction{wireID: 7, toolCallID: "call_p", kind: leaderKindPermission, perm: &p})
	if r.consumedByWire(7) {
		t.Error("replay did not clear the wire tombstone")
	}
	if _, ok := r.getByWire(7); !ok {
		t.Error("replay did not restore the wire index")
	}
}
