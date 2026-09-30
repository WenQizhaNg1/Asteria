// Package llm adapts Chat Completions-compatible services to the model port.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Chat struct {
	config   Config
	endpoint string
	client   *http.Client
}

// HTTPError exposes status for callers without retaining a provider's response
// body, which may contain prompts or credentials.
type HTTPError struct {
	Provider   string
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s model service returned HTTP %d", e.Provider, e.StatusCode)
}

func (c *Chat) Complete(ctx context.Context, instruction string, input any, output any) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	parameters := map[string]any{
		"model":           c.config.Model,
		"messages":        []map[string]string{{"role": "system", "content": instruction + "\nReturn only a JSON object matching the requested structure."}, {"role": "user", "content": string(payload)}},
		"response_format": map[string]string{"type": "json_object"},
		"max_tokens":      c.config.MaxTokens,
		"stream":          false,
	}
	switch c.config.Provider {
	case "openrouter":
		// Prevent routing to an upstream that silently ignores JSON mode.
		parameters["provider"] = map[string]bool{"require_parameters": true}
	case "qwen":
		// Many Qwen models support JSON mode only without thinking.
		parameters["enable_thinking"] = false
	case "kimi":
		// Moonshot deprecates max_tokens in favor of this parameter.
		delete(parameters, "max_tokens")
		parameters["max_completion_tokens"] = c.config.MaxTokens
	}
	body, err := json.Marshal(parameters)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("model request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Don't forward provider response bodies: they may contain prompt text.
		return &HTTPError{Provider: c.config.Provider, StatusCode: response.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 2<<20 {
		return fmt.Errorf("model response exceeds 2 MiB")
	}
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Error   json.RawMessage `json:"error"`
			Message struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("invalid model response envelope: %w", err)
	}
	hasError := func(value json.RawMessage) bool {
		return len(value) != 0 && string(value) != "null"
	}
	if hasError(envelope.Error) || len(envelope.Choices) == 0 || hasError(envelope.Choices[0].Error) || envelope.Choices[0].Message.Refusal != "" || envelope.Choices[0].FinishReason != "stop" {
		return fmt.Errorf("model returned no complete structured answer")
	}
	content := strings.TrimSpace(envelope.Choices[0].Message.Content)
	if !strings.HasPrefix(content, "{") {
		return fmt.Errorf("model must return a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("model returned invalid structured JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("model returned trailing data")
	}
	return nil
}
