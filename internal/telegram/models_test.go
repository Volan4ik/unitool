package telegram

import (
	"reflect"
	"testing"
)

func TestResolveModelByMode(t *testing.T) {
	id, ok := ResolveModel("text", "GPT-5 Nano")
	if !ok || id == "" {
		t.Fatal("expected text model to resolve")
	}

	id, ok = ResolveModel("image", "Kling")
	if !ok || id != "kling-v2" {
		t.Fatalf("expected image Kling to resolve to kling-v2, got id=%q ok=%v", id, ok)
	}

	id, ok = ResolveModel("video", "Sora 2")
	if !ok || id == "" {
		t.Fatal("expected video model to resolve")
	}

	id, ok = ResolveModel("video", "Veo 3")
	if !ok || id != "veo3.1" {
		t.Fatalf("expected Veo 3 to resolve to veo3.1, got id=%q ok=%v", id, ok)
	}

	id, ok = ResolveModel("video", "Kling")
	if !ok || id != "kling-v1-6" {
		t.Fatalf("expected Kling to resolve to kling-v1-6, got id=%q ok=%v", id, ok)
	}
}

func TestModelUIListStableOrder(t *testing.T) {
	tests := []struct {
		mode string
		want []string
	}{
		{
			mode: "text",
			want: []string{"GPT-5 Nano", "Gemini 2.5 Flash", "Grok 3", "Deepseek"},
		},
		{
			mode: "image",
			want: []string{"GPT 4o Image", "Gemini 2.5 (Nano Banana)", "Kling"},
		},
		{
			mode: "video",
			want: []string{"Sora 2", "Kling", "Veo 3"},
		},
	}

	for _, tc := range tests {
		got := ModelUIList(tc.mode)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("mode=%s: unexpected order: got=%v want=%v", tc.mode, got, tc.want)
		}
	}
}

func TestDefaultModelUIExplicitAndStable(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{mode: "text", want: "GPT-5 Nano"},
		{mode: "image", want: "GPT 4o Image"},
		{mode: "video", want: "Sora 2"},
	}

	for _, tc := range tests {
		first, ok := DefaultModelUI(tc.mode)
		if !ok || first != tc.want {
			t.Fatalf("mode=%s: unexpected default: got=%q ok=%v want=%q", tc.mode, first, ok, tc.want)
		}
		second, ok := DefaultModelUI(tc.mode)
		if !ok || second != first {
			t.Fatalf("mode=%s: default changed between calls: first=%q second=%q", tc.mode, first, second)
		}
	}
}

func TestModelUIListNotEmpty(t *testing.T) {
	if len(ModelUIList("text")) == 0 {
		t.Fatal("text model list must not be empty")
	}
	if len(ModelUIList("image")) == 0 {
		t.Fatal("image model list must not be empty")
	}
	if len(ModelUIList("video")) == 0 {
		t.Fatal("video model list must not be empty")
	}
}
