package provider

import "context"

type ModelRequest struct {
	UserID int64
	Input  string
	Params map[string]any
}

type ModelResponse struct {
	Output string
	Tokens int
	Meta   map[string]any
}

type ModelProvider interface {
	Generate(ctx context.Context, req ModelRequest) (ModelResponse, error)
}