package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestChatCompatibleProtocolAndStructuredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("incorrect model request: %s %s", r.Method, r.URL.Path)
		}
		var request struct {
			Model     string                           `json:"model"`
			Messages  []struct{ Role, Content string } `json:"messages"`
			MaxTokens int                              `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "test-model" || len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.MaxTokens != 4096 {
			t.Errorf("model context is malformed: %+v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]string{"content": `{"done":true}`}, "finish_reason": "stop",
		}}})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL + "/v1", Model: "test-model", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Done bool `json:"done"`
	}
	if err := client.Complete(context.Background(), "structured instruction", map[string]string{"question": "停车"}, &output); err != nil || !output.Done {
		t.Fatalf("model response was not decoded: %+v %v", output, err)
	}
}

func TestProviderErrorsDoNotExposePromptOrCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "private-prompt and secret-key", http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, _ := New(Config{Provider: "openrouter", BaseURL: server.URL, Model: "test", APIKey: "secret-key"})
	var output any
	err := client.Complete(context.Background(), "private-prompt", nil, &output)
	if err == nil || !strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "private-prompt") {
		t.Fatalf("provider error leaked data: %v", err)
	}
	var status *HTTPError
	if !errors.As(err, &status) || status.StatusCode != http.StatusTooManyRequests || status.Provider != "openrouter" {
		t.Fatalf("provider status is unavailable: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestMaintainedProviderContracts(t *testing.T) {
	for _, tc := range []struct{ provider, endpoint string }{
		{"openrouter", "https://openrouter.ai/api/v1/chat/completions"},
		{"deepseek", "https://api.deepseek.com/chat/completions"},
		{"qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"},
		{"kimi", "https://api.moonshot.cn/v1/chat/completions"},
		{"glm", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer provider-key" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("provider authentication or request method is incorrect")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body["model"] != "vendor/structured-model" || body["stream"] != false {
					t.Errorf("incorrect model or streaming mode: %v", body)
				}
				format, _ := body["response_format"].(map[string]any)
				if format["type"] != "json_object" {
					t.Error("JSON mode was not requested")
				}
				messages, _ := body["messages"].([]any)
				if len(messages) != 2 {
					t.Errorf("expected two messages: %v", messages)
				} else {
					system, _ := messages[0].(map[string]any)
					user, _ := messages[1].(map[string]any)
					content, _ := system["content"].(string)
					if system["role"] != "system" || !strings.Contains(strings.ToLower(content), "json") || !strings.Contains(content, "Read the knowledge") || user["content"] != `{"question":"停车"}` {
						t.Errorf("instruction or input was lost: %v", messages)
					}
				}
				limitField, absentLimitField := "max_tokens", "max_completion_tokens"
				if tc.provider == "kimi" {
					limitField, absentLimitField = absentLimitField, limitField
				}
				if body[limitField] != float64(8192) || body[absentLimitField] != nil {
					t.Errorf("incorrect token limit: %v", body)
				}
				if tc.provider == "openrouter" {
					routing, _ := body["provider"].(map[string]any)
					if routing["require_parameters"] != true {
						t.Error("OpenRouter may route to an upstream without JSON support")
					}
				} else if _, present := body["provider"]; present {
					t.Error("OpenRouter parameters leaked to another provider")
				}
				if tc.provider == "qwen" {
					if body["enable_thinking"] != false {
						t.Error("Qwen JSON mode must disable thinking")
					}
				} else if _, present := body["enable_thinking"]; present {
					t.Error("Qwen parameters leaked to another provider")
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"{\"done\":true}","reasoning_content":"ignored"},"finish_reason":"stop"}],"usage":{"total_tokens":42}}`)
			}))
			defer server.Close()
			client, err := New(Config{Provider: strings.ToUpper(tc.provider), Model: "vendor/structured-model", APIKey: "provider-key", MaxTokens: 8192})
			if err != nil {
				t.Fatal(err)
			}
			local, _ := url.Parse(server.URL)
			// Check the real endpoint, then send only to the local contract server.
			client.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != tc.endpoint {
					t.Errorf("endpoint = %s, want %s", r.URL, tc.endpoint)
				}
				copy := r.Clone(r.Context())
				copy.URL.Scheme, copy.URL.Host = local.Scheme, local.Host
				return http.DefaultTransport.RoundTrip(copy)
			})
			var output struct {
				Done bool `json:"done"`
			}
			if err := client.Complete(context.Background(), "Read the knowledge", map[string]string{"question": "停车"}, &output); err != nil || !output.Done {
				t.Fatalf("structured completion failed: %+v %v", output, err)
			}
		})
	}
}

func TestChatRejectsIncompleteAndInvalidResponses(t *testing.T) {
	for name, response := range map[string]string{
		"truncated":      `{"choices":[{"message":{"content":"{}"},"finish_reason":"length"}]}`,
		"filtered":       `{"choices":[{"message":{"content":"{}"},"finish_reason":"content_filter"}]}`,
		"tools":          `{"choices":[{"message":{"content":"{}"},"finish_reason":"tool_calls"}]}`,
		"unfinished":     `{"choices":[{"message":{"content":"{}"}}]}`,
		"refused":        `{"choices":[{"message":{"content":"{}","refusal":"denied"},"finish_reason":"stop"}]}`,
		"provider error": `{"error":{"code":429,"message":"private-prompt secret-key"},"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`,
		"choice error":   `{"choices":[{"error":{"message":"private-prompt secret-key"},"message":{"content":"{}"},"finish_reason":"stop"}]}`,
		"empty choices":  `{"choices":[]}`,
		"null":           `{"choices":[{"message":{"content":"null"},"finish_reason":"stop"}]}`,
		"array":          `{"choices":[{"message":{"content":"[]"},"finish_reason":"stop"}]}`,
		"empty content":  `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`,
		"unknown field":  `{"choices":[{"message":{"content":"{\"extra\":true}"},"finish_reason":"stop"}]}`,
		"trailing data":  `{"choices":[{"message":{"content":"{} {}"},"finish_reason":"stop"}]}`,
		"invalid JSON":   `{"choices":[{"message":{"content":"{"},"finish_reason":"stop"}]}`,
		"fenced JSON":    "{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"done\\\":true}\\n```\"},\"finish_reason\":\"stop\"}]}",
		"oversized":      strings.Repeat(" ", (2<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, response) }))
			defer server.Close()
			client, _ := New(Config{BaseURL: server.URL, Model: "test"})
			var output struct {
				Done bool `json:"done"`
			}
			err := client.Complete(context.Background(), "private-prompt", nil, &output)
			if err == nil || strings.Contains(err.Error(), "private-prompt") || strings.Contains(err.Error(), "secret-key") {
				t.Fatalf("response was accepted or leaked provider content: %v", err)
			}
		})
	}
}

func TestChatCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, Model: "test", Timeout: 20 * time.Millisecond})
	var output any
	if err := client.Complete(context.Background(), "Return JSON", nil, &output); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("model timeout did not propagate: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Complete(ctx, "Return JSON", nil, &output); !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation did not propagate: %v", err)
	}
}
