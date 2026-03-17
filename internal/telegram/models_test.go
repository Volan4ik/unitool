package telegram

import "testing"

func TestResolveModelByMode(t *testing.T) {
	id, ok := ResolveModel("text", "GPT-5 Nano")
	if !ok || id == "" {
		t.Fatal("expected text model to resolve")
	}

	id, ok = ResolveModel("image", "DALL-E 3")
	if !ok || id == "" {
		t.Fatal("expected image model to resolve")
	}

	id, ok = ResolveModel("video", "Sora 2")
	if !ok || id == "" {
		t.Fatal("expected video model to resolve")
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
