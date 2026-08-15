package codex

import (
	"reflect"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// §3.9 truth source: staged image paths are preserved in BOTH exec and
// app_server modes — image+file declared unconditionally.
func TestSupportedAttachmentKinds(t *testing.T) {
	a := &Agent{}
	if got := a.SupportedAttachmentKinds(); !reflect.DeepEqual(got, []string{"image", "file"}) {
		t.Fatalf("kinds = %v, want [image file]", got)
	}
	var _ core.AttachmentSupporter = (*Agent)(nil)
}
