package comet

import "testing"

func TestBuildKlingVideoRequest(t *testing.T) {
	t.Run("text to video without references", func(t *testing.T) {
		path, payload := buildKlingVideoRequest("kling-v1-6", "move", nil)
		if path != "/kling/v1/videos/text2video" {
			t.Fatalf("unexpected path: %q", path)
		}
		if payload.Prompt != "move" || payload.ModelName != "kling-v1-6" || payload.Duration != "5" {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		if payload.Image != "" || len(payload.ImageList) != 0 {
			t.Fatalf("unexpected refs in payload: %+v", payload)
		}
	})

	t.Run("image to video with one reference", func(t *testing.T) {
		path, payload := buildKlingVideoRequest("kling-v1-6", "move", []string{"data:image/png;base64,QUJD"})
		if path != "/kling/v1/videos/image2video" {
			t.Fatalf("unexpected path: %q", path)
		}
		if payload.Image != "QUJD" {
			t.Fatalf("unexpected image: %q", payload.Image)
		}
		if len(payload.ImageList) != 0 {
			t.Fatalf("image list must be empty: %+v", payload.ImageList)
		}
	})

	t.Run("multi image to video with multiple references", func(t *testing.T) {
		path, payload := buildKlingVideoRequest("kling-v1-6", "move", []string{
			" first ",
			"data:image/jpeg;base64,U0VDT05E",
			"third",
			"fourth",
			"fifth",
		})
		if path != "/kling/v1/videos/multi-image2video" {
			t.Fatalf("unexpected path: %q", path)
		}
		if payload.Image != "" {
			t.Fatalf("single image must be empty: %q", payload.Image)
		}
		if len(payload.ImageList) != 4 {
			t.Fatalf("unexpected image list: %+v", payload.ImageList)
		}
		if payload.ImageList[0].Image != "first" || payload.ImageList[1].Image != "U0VDT05E" {
			t.Fatalf("unexpected normalized refs: %+v", payload.ImageList)
		}
		if payload.ImageList[3].Image != "fourth" {
			t.Fatalf("expected provider-side cap at four refs: %+v", payload.ImageList)
		}
	})
}

func TestInputReferencesFromParams(t *testing.T) {
	refs := inputReferencesFromParams(map[string]any{
		"input_reference":  "fallback",
		"input_references": []string{" one ", "", "two"},
	})
	if len(refs) != 2 || refs[0] != "one" || refs[1] != "two" {
		t.Fatalf("unexpected refs: %+v", refs)
	}

	refs = inputReferencesFromParams(map[string]any{"input_reference": " single "})
	if len(refs) != 1 || refs[0] != "single" {
		t.Fatalf("unexpected fallback refs: %+v", refs)
	}
}
