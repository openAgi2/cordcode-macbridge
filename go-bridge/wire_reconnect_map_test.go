package gobridge

// §8.5 wire mapping (canonical-3080 design §3.2/§12.1-1): the dsh-web
// seat-grace typed error maps to backend_unavailable at every send/list entry
// point — never send_failed/list_failed, and never not_configured. Plain
// errors keep their current codes. The detector row guards the §12.1-4 chain:
// available=true during grace must survive detectInstanceStatusProber.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	dshweb "github.com/openAgi2/cordcode-macbridge/agent/dsh-web"
	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestWireErrorWithReconnectMapping(t *testing.T) {
	typed := &dshweb.ErrInstanceReconnecting{
		BaseURL: "http://127.0.0.1:3080",
		Until:   time.Now().Add(90 * time.Second),
	}
	wrapped := fmt.Errorf("send: %w", typed)
	plain := errors.New("official RpcError text")

	cases := []struct {
		name    string
		err     error
		want    string
		wantMsg string
	}{
		{"typed send → backend_unavailable", typed, "backend_unavailable", "reconnecting"},
		{"wrapped typed → backend_unavailable", wrapped, "backend_unavailable", "reconnecting"},
		{"plain send keeps send_failed", plain, "send_failed", "official RpcError text"},
		{"starting variant → backend_unavailable", &dshweb.ErrInstanceReconnecting{BaseURL: "http://127.0.0.1:3080", Starting: true}, "backend_unavailable", "starting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sendWireError(tc.err)
			if got.Code != tc.want {
				t.Fatalf("send code = %s, want %s", got.Code, tc.want)
			}
			if got.Message == "" {
				t.Fatal("message must carry the underlying text")
			}
			list := listWireError(tc.err)
			wantList := tc.want
			if wantList == "send_failed" {
				wantList = "list_failed"
			}
			if list.Code != wantList {
				t.Fatalf("list code = %s, want %s", list.Code, wantList)
			}
		})
	}
}

// reconnectProberStub feeds detectInstanceStatusProber the grace-shape status.
type reconnectProberStub struct {
	core.Agent // embed for the interface; only InstanceStatus is called
}

func (reconnectProberStub) InstanceStatus() (bool, string) {
	return true, "instance reconnecting (grace until 2026-08-19T21:00:00+08:00)"
}

func TestDetectorKeepsGraceStatusAvailable(t *testing.T) {
	// §12.1-4 chain guard: available=true must map to AgentStatusAvailable —
	// the detector folds ANY available=false into not_configured, which is
	// why InstanceStatus special-cases the grace window.
	status, reason := detectInstanceStatusProber("dsh-web", reconnectProberStub{})
	if status != AgentStatusAvailable {
		t.Fatalf("grace status must stay available, got %s", status)
	}
	if reason == "" {
		t.Fatal("reconnecting detail must survive the detector")
	}
}
