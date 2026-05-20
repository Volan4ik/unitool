package telegram

import (
	"reflect"
	"testing"
)

func TestModelUIListByModeAndFallback(t *testing.T) {
	image := ModelUIList("image")
	wantImage := []string{"GPT Image 2", "Nano Banana", "Kling"}
	if !reflect.DeepEqual(image, wantImage) {
		t.Fatalf("image list mismatch: got=%v want=%v", image, wantImage)
	}

	video := ModelUIList("video")
	wantVideo := []string{"Sora 2", "Seedance 2.0", "Kling v2", "Veo 3"}
	if !reflect.DeepEqual(video, wantVideo) {
		t.Fatalf("video list mismatch: got=%v want=%v", video, wantVideo)
	}

	unknown := ModelUIList("unknown")
	text := ModelUIList("text")
	if !reflect.DeepEqual(unknown, text) {
		t.Fatalf("unknown mode must fallback to text list: got=%v text=%v", unknown, text)
	}
}

func TestDefaultModelUI(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{mode: "text", want: "GPT-5 Nano"},
		{mode: "image", want: "GPT Image 2"},
		{mode: "video", want: "Sora 2"},
		{mode: "unknown", want: "GPT-5 Nano"},
	}

	for _, tc := range cases {
		got, ok := DefaultModelUI(tc.mode)
		if !ok {
			t.Fatalf("expected default for mode=%s", tc.mode)
		}
		if got != tc.want {
			t.Fatalf("default mismatch mode=%s got=%q want=%q", tc.mode, got, tc.want)
		}
	}
}

func TestResolveModel(t *testing.T) {
	got, ok := ResolveModel("image", "GPT Image 2")
	if !ok || got != "gpt-image-2" {
		t.Fatalf("resolve default image model failed: ok=%v got=%q", ok, got)
	}

	got, ok = ResolveModel("image", "Nano Banana")
	if !ok || got != "gemini-3.1-flash-image-preview" {
		t.Fatalf("resolve image model failed: ok=%v got=%q", ok, got)
	}

	got, ok = ResolveModel("video", "Kling")
	if !ok || got != "kling-v1-6" {
		t.Fatalf("resolve video model failed: ok=%v got=%q", ok, got)
	}

	got, ok = ResolveModel("video", "Doubao Seedance 2.0")
	if !ok || got != "doubao-seedance-2-0" {
		t.Fatalf("resolve doubao video model failed: ok=%v got=%q", ok, got)
	}

	if got, ok = ResolveModel("image", "Nope"); ok || got != "" {
		t.Fatalf("unknown model must not resolve: ok=%v got=%q", ok, got)
	}
}
