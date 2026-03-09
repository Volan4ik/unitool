package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Result struct {
	Allowed bool
	Reasons []string
}

type Client interface {
	CheckPrompt(ctx context.Context, prompt string) (Result, error)
}

type OpenAIClient struct {
	base  string
	key   string
	model string
	httpc *http.Client
}

func NewOpenAIClient(base, key, model string, timeout time.Duration) *OpenAIClient {
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	base = strings.TrimSpace(base)
	if base == "" {
		base = "https://api.openai.com"
	}
	if model == "" {
		model = "omni-moderation-latest"
	}
	return &OpenAIClient{
		base:  strings.TrimRight(base, "/"),
		key:   key,
		model: model,
		httpc: &http.Client{Timeout: timeout},
	}
}

type moderationReq struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type moderationResp struct {
	Results []struct {
		Flagged    bool            `json:"flagged"`
		Categories map[string]bool `json:"categories"`
	} `json:"results"`
}

func (c *OpenAIClient) CheckPrompt(ctx context.Context, prompt string) (Result, error) {
	if strings.TrimSpace(prompt) == "" {
		return Result{Allowed: true}, nil
	}
	if c.key == "" {
		return Result{}, fmt.Errorf("openai moderation api key is empty")
	}

	body, err := json.Marshal(moderationReq{
		Model: c.model,
		Input: prompt,
	})
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/moderations", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("moderation http %d: %s", resp.StatusCode, string(b))
	}

	var out moderationResp
	if err := json.Unmarshal(b, &out); err != nil {
		return Result{}, err
	}
	if len(out.Results) == 0 {
		return Result{}, fmt.Errorf("moderation returned no results")
	}

	r0 := out.Results[0]
	reasons := nsfwReasons(r0.Categories, r0.Flagged)
	return Result{
		Allowed: len(reasons) == 0,
		Reasons: reasons,
	}, nil
}

func nsfwReasons(categories map[string]bool, flagged bool) []string {
	var reasons []string
	if categories["sexual"] {
		reasons = append(reasons, "sexual")
	}
	if categories["sexual/minors"] || categories["sexual_minors"] {
		reasons = append(reasons, "sexual/minors")
	}
	if categories["violence/graphic"] || categories["violence_graphic"] {
		reasons = append(reasons, "violence/graphic")
	}
	if flagged && len(reasons) == 0 {
		reasons = append(reasons, "flagged")
	}
	return reasons
}
