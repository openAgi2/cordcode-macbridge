package gobridge

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func pngB64() string { return base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4E, 0x47}) }
func pdfB64() string { return base64.StdEncoding.EncodeToString([]byte{0x25, 0x50, 0x44, 0x46}) }
func anyB64() string { return base64.StdEncoding.EncodeToString([]byte{0x1, 0x2}) }

func TestSplitAttachments_ImageAndFile(t *testing.T) {
	inputs := []AttachmentInput{
		{Kind: "image", Mime: "image/png", Filename: "a.png", Base64: pngB64()},
		{Kind: "file", Mime: "application/pdf", Filename: "a.pdf", Base64: pdfB64()},
	}
	images, files, err := splitAttachments(inputs)
	if err != nil {
		t.Fatalf("valid input must split: %v", err)
	}
	if len(images) != 1 || len(files) != 1 {
		t.Fatalf("want 1 image + 1 file, got %d images, %d files", len(images), len(files))
	}
	if images[0].MimeType != "image/png" || !bytes.Equal(images[0].Data, []byte{0x89, 0x50, 0x4E, 0x47}) {
		t.Errorf("unexpected image: mime=%s data=%v", images[0].MimeType, images[0].Data)
	}
	if images[0].FileName != "a.png" {
		t.Errorf("image filename not preserved: %q", images[0].FileName)
	}
	if files[0].MimeType != "application/pdf" || !bytes.Equal(files[0].Data, []byte{0x25, 0x50, 0x44, 0x46}) {
		t.Errorf("unexpected file: mime=%s data=%v", files[0].MimeType, files[0].Data)
	}
}

func TestSplitAttachments_EffectiveKindSharedWithGate(t *testing.T) {
	// The classification in split MUST equal classifyAttachment — the same
	// judgment the pre-check gate uses (round11 P0-1: no second opinion).
	jpg := base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF})
	inputs := []AttachmentInput{
		{Kind: "", Mime: "image/jpeg", Base64: jpg}, // legacy lenient shape: still image downstream
	}
	if got := classifyAttachment(inputs[0]); got != "image" {
		t.Fatalf("classifyAttachment = %q, want image", got)
	}
	images, files, err := splitAttachments(inputs)
	if err != nil {
		t.Fatalf("split of decodable legacy shape must succeed: %v", err)
	}
	if len(images) != 1 || len(files) != 0 {
		t.Fatalf("mime image/* classifies image; got %d images %d files", len(images), len(files))
	}
}

func TestSplitAttachments_RejectsInvalid(t *testing.T) {
	cases := []struct {
		name  string
		input []AttachmentInput
	}{
		{"empty base64", []AttachmentInput{{Kind: "image", Mime: "image/png", Base64: ""}}},
		{"invalid base64", []AttachmentInput{{Kind: "image", Mime: "image/png", Base64: "@@notb64@@"}}},
	}
	for _, tc := range cases {
		images, files, err := splitAttachments(tc.input)
		if err == nil {
			t.Fatalf("%s: want validation error, got %d images %d files", tc.name, len(images), len(files))
		}
	}
}

func TestSplitAttachments_NilEmpty(t *testing.T) {
	images, files, err := splitAttachments(nil)
	if err != nil {
		t.Fatalf("nil input: %v", err)
	}
	if images != nil || files != nil {
		t.Errorf("nil input should return nil slices, got images=%v files=%v", images, files)
	}
}

func TestClassifyAttachmentSingleRule(t *testing.T) {
	cases := []struct {
		kind, mime, want string
	}{
		{"image", "image/png", "image"},
		{"image", "text/plain", "image"}, // kind wins
		{"file", "image/png", "image"},   // mime prefix wins (round11 fixture)
		{"file", "application/pdf", "file"},
		{"file", "IMAGE/PNG", "image"}, // normalized mime prefix
		{"", "image/jpeg", "image"},    // legacy lenient shape
		{"", "text/csv", "file"},
		{"file", "  image/png  ", "image"}, // trimmed
	}
	for _, tc := range cases {
		got := classifyAttachment(AttachmentInput{Kind: tc.kind, Mime: tc.mime})
		if got != tc.want {
			t.Errorf("classify(%q,%q) = %q, want %q", tc.kind, tc.mime, got, tc.want)
		}
	}
}

func TestValidateAttachmentStructure(t *testing.T) {
	cases := []struct {
		name    string
		input   AttachmentInput
		wantErr bool
	}{
		{"valid image", AttachmentInput{Kind: "image", Mime: "image/png", Base64: pngB64()}, false},
		{"valid file", AttachmentInput{Kind: "file", Mime: "application/pdf", Base64: pdfB64()}, false},
		{"uppercase mime ok", AttachmentInput{Kind: "file", Mime: "Application/PDF", Base64: pdfB64()}, false},
		{"empty kind", AttachmentInput{Kind: "", Mime: "text/plain", Base64: anyB64()}, true},
		{"unknown kind", AttachmentInput{Kind: "video", Mime: "video/mp4", Base64: anyB64()}, true},
		{"empty mime", AttachmentInput{Kind: "file", Base64: anyB64()}, true},
		{"not-a-mime", AttachmentInput{Kind: "file", Mime: "not-a-mime", Base64: anyB64()}, true},
		{"bare type", AttachmentInput{Kind: "file", Mime: "image", Base64: anyB64()}, true},
		{"mime with params", AttachmentInput{Kind: "file", Mime: "image/png; charset=utf-8", Base64: anyB64()}, true},
		{"mime wildcard literal", AttachmentInput{Kind: "file", Mime: "image/*", Base64: anyB64()}, true},
		{"empty base64", AttachmentInput{Kind: "file", Mime: "text/plain", Base64: ""}, true},
		{"bad base64", AttachmentInput{Kind: "file", Mime: "text/plain", Base64: "!!"}, true},
	}
	for _, tc := range cases {
		err := validateAttachmentStructure([]AttachmentInput{tc.input})
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", tc.name, err, tc.wantErr)
		}
	}

	// Mixed valid + invalid: the WHOLE batch is rejected.
	err := validateAttachmentStructure([]AttachmentInput{
		{Kind: "image", Mime: "image/png", Base64: pngB64()},
		{Kind: "file", Mime: "not-a-mime", Base64: anyB64()},
	})
	if err == nil {
		t.Fatal("mixed batch must be rejected as a whole")
	}
}
