package telegram

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestLargestPhotoFileID(t *testing.T) {
	if got := largestPhotoFileID(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	photos := []tgbotapi.PhotoSize{
		{FileID: "small", Width: 100, Height: 100},
		{FileID: "wide", Width: 300, Height: 80},
		{FileID: "big", Width: 500, Height: 400},
	}
	if got := largestPhotoFileID(photos); got != "big" {
		t.Fatalf("got %q", got)
	}

	// Equal area should pick the last one because of >= comparison.
	photos = []tgbotapi.PhotoSize{
		{FileID: "first", Width: 100, Height: 100},
		{FileID: "second", Width: 200, Height: 50},
	}
	if got := largestPhotoFileID(photos); got != "second" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildPromptWithReferences(t *testing.T) {
	got := buildPromptWithReferences("make it cinematic", []string{"data:image/png;base64,AAA", "  ", "data:image/jpeg;base64,BBB"})
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected lines count=%d prompt=%q", len(lines), got)
	}
	if lines[0] != "input_reference=data:image/png;base64,AAA" {
		t.Fatalf("unexpected first line: %q", lines[0])
	}
	if lines[2] != "make it cinematic" {
		t.Fatalf("unexpected prompt line: %q", lines[2])
	}

	if same := buildPromptWithReferences("prompt only", nil); same != "prompt only" {
		t.Fatalf("unexpected prompt: %q", same)
	}
}

func TestReferenceFileIDsForModeModel(t *testing.T) {
	t.Run("image keeps all references", func(t *testing.T) {
		refs, warning := referenceFileIDsForModeModel("image", "kling-v2", []string{"a", "b"})
		if warning != "" || len(refs) != 2 {
			t.Fatalf("unexpected refs=%v warning=%q", refs, warning)
		}
	})

	t.Run("sora video keeps first reference", func(t *testing.T) {
		refs, warning := referenceFileIDsForModeModel("video", "sora-2", []string{"a", "b"})
		if len(refs) != 1 || refs[0] != "a" || warning == "" {
			t.Fatalf("unexpected refs=%v warning=%q", refs, warning)
		}
	})

	t.Run("kling video keeps up to four references", func(t *testing.T) {
		refs, warning := referenceFileIDsForModeModel("video", "kling-v2-6", []string{"a", "b", "c", "d", "e"})
		if len(refs) != 4 || refs[0] != "a" || refs[3] != "d" || warning == "" {
			t.Fatalf("unexpected refs=%v warning=%q", refs, warning)
		}
	})
}

func TestPromptWithoutReferenceLines(t *testing.T) {
	raw := strings.Join([]string{
		"input_reference=data:image/png;base64,AAA",
		"subject in neon city",
		"  input_reference: https://example.com/image.jpg ",
		"camera: close-up",
	}, "\n")
	got := promptWithoutReferenceLines(raw)
	if strings.Contains(strings.ToLower(got), "input_reference") {
		t.Fatalf("reference line leaked: %q", got)
	}
	if !strings.Contains(got, "subject in neon city") || !strings.Contains(got, "camera: close-up") {
		t.Fatalf("prompt lines missing: %q", got)
	}
}

func TestParseTelegramInputReferenceLine(t *testing.T) {
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{line: "input_reference=data:image/png;base64,AAA", want: "data:image/png;base64,AAA", ok: true},
		{line: " input_reference: <https://example.com/a.png> ", want: "https://example.com/a.png", ok: true},
		{line: `input_reference: "https://example.com/b.png"`, want: "https://example.com/b.png", ok: true},
		{line: "input_reference", want: "", ok: false},
		{line: "input_reference-", want: "", ok: false},
		{line: "other=value", want: "", ok: false},
	}
	for _, tc := range cases {
		got, ok := parseTelegramInputReferenceLine(tc.line)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("line=%q got=(%q,%v) want=(%q,%v)", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFitVideoReferenceDataURI(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 300, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			src.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 80, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	got, err := fitVideoReferenceDataURI(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Fatalf("unexpected data URI prefix: %q", got[:32])
	}

	comma := strings.IndexByte(got, ',')
	if comma <= 0 {
		t.Fatalf("invalid data URI: %q", got[:32])
	}
	fittedBytes, err := base64.StdEncoding.DecodeString(got[comma+1:])
	if err != nil {
		t.Fatal(err)
	}
	fitted, _, err := image.Decode(bytes.NewReader(fittedBytes))
	if err != nil {
		t.Fatal(err)
	}
	bounds := fitted.Bounds()
	if bounds.Dx() != videoReferenceWidth || bounds.Dy() != videoReferenceHeight {
		t.Fatalf("size=%dx%d want=%dx%d", bounds.Dx(), bounds.Dy(), videoReferenceWidth, videoReferenceHeight)
	}
}
