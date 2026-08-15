package grokbuild

import (
	"reflect"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// §3.9 truth source: file paths join the prompt text; the ACP image block
// carries no bytes and Grok freezes promptCapabilities.image=false —
// file-only, declaring image would be a false capability.
func TestSupportedAttachmentKinds(t *testing.T) {
	a := &Agent{}
	if got := a.SupportedAttachmentKinds(); !reflect.DeepEqual(got, []string{"file"}) {
		t.Fatalf("kinds = %v, want [file]", got)
	}
	var _ core.AttachmentSupporter = (*Agent)(nil)
}
