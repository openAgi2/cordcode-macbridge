package dsh

import (
	"errors"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// §3.9/§8: DSH is text-only — it does NOT implement AttachmentSupporter (no
// image/file capability is advertised), and the driver-level defense-in-depth
// refuses non-empty slices with a stable sentinel.
func TestDSHTextOnlyAttachments(t *testing.T) {
	var agent core.Agent = &Agent{}
	if _, ok := agent.(core.AttachmentSupporter); ok {
		t.Fatal("dsh must not implement AttachmentSupporter (text-only)")
	}
	wd := (&Agent{}).WireDescriptor()
	if wd == nil {
		t.Fatal("wire descriptor required")
	}
	for _, c := range wd.StaticCapabilities {
		if c == "image" || c == "file" {
			t.Fatalf("dsh must not declare attachment capability %q", c)
		}
	}

	s := &dshSession{}
	if err := s.Send("x", []core.ImageAttachment{{MimeType: "image/png"}}, nil); !errors.Is(err, errAttachmentsNotSupported) {
		t.Fatalf("image slice must be refused: %v", err)
	}
	if err := s.Send("x", nil, []core.FileAttachment{{MimeType: "application/pdf"}}); !errors.Is(err, errAttachmentsNotSupported) {
		t.Fatalf("file slice must be refused: %v", err)
	}
}
