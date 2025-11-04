package provider

import "context"

// Message represents a simple role-content pair for chat-like models.
type Message struct {
    Role    string
    Content string
}

type ModelRequest struct {
    UserID  int64
    Input   string
    Model   string
    History []Message
    Params  map[string]any
}

type ModelResponse struct {
    Output string
    Tokens int
    Meta   map[string]any
}

type ModelProvider interface {
    Generate(ctx context.Context, req ModelRequest) (ModelResponse, error)
    // GenerateStream streams partial outputs for chat-like models.
    // The onDelta callback is called with each content piece.
    // Implementations should also accumulate and return the final output.
    GenerateStream(ctx context.Context, req ModelRequest, onDelta func(string) error) (ModelResponse, error)
}
