package credits

import "testing"

func TestGenerationCost(t *testing.T) {
	tests := []struct {
		name  string
		kind  string
		model string
		want  int32
	}{
		{name: "seedance video costs two", kind: "video", model: SeedanceVideoModel, want: 2},
		{name: "other video costs one", kind: "video", model: "kling-v2", want: 1},
		{name: "image costs one", kind: "image", model: "any", want: 1},
		{name: "text costs one", kind: "text", model: "any", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GenerationCost(tt.kind, tt.model); got != tt.want {
				t.Fatalf("GenerationCost(%q, %q) = %d, want %d", tt.kind, tt.model, got, tt.want)
			}
		})
	}
}
