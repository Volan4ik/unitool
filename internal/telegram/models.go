package telegram

// Registry of supported UI model names mapped to provider model IDs.
var (
	modelText = map[string]string{
		"GPT-5 Nano":       "gpt-5-nano",
		"Gemini 2.5 Flash": "gemini-2.5-flash-lite",
		"Grok 3":           "grok-3-mini",
		"Deepseek":         "deepseek-chat",
	}

	// Image generation models (kind = "image").
	modelImage = map[string]string{
		"GPT 4o Image":             "gpt-4o-image",
		"Gemini 2.5 (Nano Banana)": "gemini-2.5-flash-image-preview",
		"DALL-E 3":                 "dall-e-3",
		"Midjourney":               "mj_fast_high_variation",
	}

	modelVideo = map[string]string{
		"Sora 2":  "sora-2",
		"Veo 3.1": "veo3.1",
	}
)

// ModelUIList returns visible UI names by mode ("text","image","video").
func ModelUIList(mode string) []string {
	var res []string
	switch mode {
	case "text":
		for k := range modelText {
			res = append(res, k)
		}
	case "image":
		for k := range modelImage {
			res = append(res, k)
		}
	case "video":
		for k := range modelVideo {
			res = append(res, k)
		}
	default:
		for k := range modelText {
			res = append(res, k)
		}
	}
	return res
}

// ResolveModel maps UI name + mode into a provider model ID.
func ResolveModel(mode, uiName string) (string, bool) {
	switch mode {
	case "text":
		id, ok := modelText[uiName]
		return id, ok
	case "image":
		id, ok := modelImage[uiName]
		return id, ok
	case "video":
		id, ok := modelVideo[uiName]
		return id, ok
	default:
		id, ok := modelText[uiName]
		return id, ok
	}
}
