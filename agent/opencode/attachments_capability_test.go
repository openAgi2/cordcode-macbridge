package opencode

import (
	"reflect"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// §3.9 mode-aware truth source: CLI mode forwards staged image paths (--file),
// so image is declared; the managed-server path drops staged image paths
// before the HTTP body — image must NOT be declared there (declaring a path
// that silently loses the image would be a false capability).
func TestSupportedAttachmentKindsModeAware(t *testing.T) {
	cli := &Agent{}
	if got := cli.SupportedAttachmentKinds(); !reflect.DeepEqual(got, []string{"file", "image"}) {
		t.Fatalf("CLI kinds = %v, want [file image]", got)
	}
	server := &Agent{httpBaseURL: "http://127.0.0.1:4096"}
	if got := server.SupportedAttachmentKinds(); !reflect.DeepEqual(got, []string{"file"}) {
		t.Fatalf("server kinds = %v, want [file]", got)
	}
	var _ core.AttachmentSupporter = (*Agent)(nil)
}
