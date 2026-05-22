package credits

import "strings"

const SeedanceVideoModel = "doubao-seedance-2-0"

func GenerationCost(kind, model string) int32 {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "video":
		if strings.EqualFold(strings.TrimSpace(model), SeedanceVideoModel) {
			return 2
		}
		return 1
	case "image", "text":
		return 1
	default:
		return 1
	}
}
