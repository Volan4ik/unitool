package telegram

// Registry of supported UI model names mapped to Comet model IDs.
// TODO: Replace placeholder IDs with actual Comet model identifiers used in your account.

var (
    // Text and Search chat models
    modelText = map[string]string{
        "GPT-5":        "gpt-4o",
        "Claude 4.5":   "claude-3-5-sonnet",
        "Gemini 2.5 Pro":"gemini-1.5-pro",
        "Grok 4":       "grok-2",
    }
    // If search uses special models in Comet, list them here
    modelSearch = map[string]string{
        "Perplexity":   "search-large", // TODO: set actual search model id
        "GPT-5":        "gpt-4o",
        "Claude 4.5":   "claude-3-5-sonnet",
        "Gemini 2.5 Pro":"gemini-1.5-pro",
        "Grok 4":       "grok-2",
    }
    // Image generation models
    modelImage = map[string]string{
        "Flux":        "flux-pro",      // TODO: set actual image model id
        "Midjourney":  "midjourney",
        "Ideogram":    "ideogram",
    }
    // Video generation models
    modelVideo = map[string]string{
        "Sora":   "sora-1",            // TODO: set actual video model id
        "Kling":  "kling-v1",
        "Hailuo": "hailuo-v1",
    }
)

// ModelUIList returns visible UI names by mode ("text","search","image","video").
func ModelUIList(mode string) []string {
    var res []string
    switch mode {
    case "text":
        for k := range modelText { res = append(res, k) }
    case "search":
        for k := range modelSearch { res = append(res, k) }
    case "image":
        for k := range modelImage { res = append(res, k) }
    case "video":
        for k := range modelVideo { res = append(res, k) }
    default:
        for k := range modelText { res = append(res, k) }
    }
    // no specific ordering guarantees; UI code can sort if necessary
    return res
}

// ResolveModel maps UI name + mode into a Comet model ID.
func ResolveModel(mode, uiName string) (string, bool) {
    switch mode {
    case "text":
        id, ok := modelText[uiName]; return id, ok
    case "search":
        id, ok := modelSearch[uiName]; return id, ok
    case "image":
        id, ok := modelImage[uiName]; return id, ok
    case "video":
        id, ok := modelVideo[uiName]; return id, ok
    default:
        id, ok := modelText[uiName]; return id, ok
    }
}

