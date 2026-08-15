package claudecode

import (
	"reflect"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// §3.9 truth source: image bytes reach the request as base64 blocks and files
// are staged to disk — both kinds positively declared.
func TestSupportedAttachmentKinds(t *testing.T) {
	a := &Agent{}
	if got := a.SupportedAttachmentKinds(); !reflect.DeepEqual(got, []string{"image", "file"}) {
		t.Fatalf("kinds = %v, want [image file]", got)
	}
	var _ core.AttachmentSupporter = (*Agent)(nil)
}
