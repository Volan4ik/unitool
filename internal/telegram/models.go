package telegram

// ModelOption describes one visible model choice in a stable order.
type ModelOption struct {
	UIName     string
	ProviderID string
	IsDefault  bool
}

// Ordered registry of supported UI model names mapped to provider model IDs.
var modelRegistry = map[string][]ModelOption{
	"text": {
		{UIName: "GPT-5 Nano", ProviderID: "gpt-5-nano", IsDefault: true},
		{UIName: "Gemini 2.5 Flash", ProviderID: "gemini-2.5-flash-lite"},
		{UIName: "Grok 3", ProviderID: "grok-3-mini"},
		{UIName: "Deepseek", ProviderID: "deepseek-chat"},
	},
	"image": {
		{UIName: "GPT 4o Image", ProviderID: "gpt-4o-image", IsDefault: true},
		{UIName: "Gemini 2.5 (Nano Banana)", ProviderID: "gemini-3.1-flash-image-preview"},
		{UIName: "Kling", ProviderID: "kling-v2"},
	},
	"video": {
		{UIName: "Sora 2", ProviderID: "sora-2", IsDefault: true},
		{UIName: "Kling", ProviderID: "kling-v1-6"},
		{UIName: "Veo 3", ProviderID: "veo3.1"},
	},
}

func modelOptions(mode string) []ModelOption {
	if opts, ok := modelRegistry[mode]; ok {
		return opts
	}
	return modelRegistry["text"]
}

// ModelUIList returns visible UI names by mode ("text","image","video").
func ModelUIList(mode string) []string {
	opts := modelOptions(mode)
	res := make([]string, 0, len(opts))
	for _, opt := range opts {
		res = append(res, opt.UIName)
	}
	return res
}

// DefaultModelUI returns the explicit default visible UI name for a mode.
func DefaultModelUI(mode string) (string, bool) {
	opts := modelOptions(mode)
	for _, opt := range opts {
		if opt.IsDefault {
			return opt.UIName, true
		}
	}
	if len(opts) == 0 {
		return "", false
	}
	return opts[0].UIName, true
}

// ResolveModel maps UI name + mode into a provider model ID.
func ResolveModel(mode, uiName string) (string, bool) {
	for _, opt := range modelOptions(mode) {
		if opt.UIName == uiName {
			return opt.ProviderID, true
		}
	}
	return "", false
}
