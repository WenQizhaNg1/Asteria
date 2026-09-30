package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"asteria/internal/core"
)

func clearModelEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"PROVIDER", "URL", "NAME", "KEY", "MAX_TOKENS", "TIMEOUT"} {
		t.Setenv("ASTERIA_MODEL_"+name, "")
	}
}

func TestModelEnvironment(t *testing.T) {
	clearModelEnv(t)
	if model, err := modelFromEnv(); model != nil || err != nil {
		t.Fatalf("empty configuration should be offline: %v", err)
	}
	for _, provider := range []string{"openrouter", "deepseek", "qwen", "kimi", "glm"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("ASTERIA_MODEL_PROVIDER", provider)
			t.Setenv("ASTERIA_MODEL_NAME", "model")
			t.Setenv("ASTERIA_MODEL_KEY", "key")
			if model, err := modelFromEnv(); model == nil || err != nil {
				t.Fatalf("provider selection failed: %v", err)
			}
		})
	}
	t.Setenv("ASTERIA_MODEL_URL", "http://localhost:11434/v1")
	t.Setenv("ASTERIA_MODEL_NAME", "local-model")
	if model, err := modelFromEnv(); model == nil || err != nil {
		t.Fatalf("legacy configuration failed: %v", err)
	}
	for name, values := range map[string][]string{
		"MAX_TOKENS": {"0", "-1", "lots"},
		"TIMEOUT":    {"0s", "-1s", "90"},
		"PROVIDER":   {"unknown"},
		"NAME":       {""},
	} {
		for _, value := range values {
			t.Run(name+"="+value, func(t *testing.T) {
				t.Setenv("ASTERIA_MODEL_"+name, value)
				if model, err := modelFromEnv(); err == nil || model != nil {
					t.Fatal("invalid configuration did not fail cleanly")
				}
			})
		}
	}
}

func TestCLIUsesProviderAndHonorsOffline(t *testing.T) {
	clearModelEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens      int   `json:"max_tokens"`
			EnableThinking *bool `json:"enable_thinking"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/custom/chat/completions" || body.MaxTokens != 2048 || body.EnableThinking == nil || *body.EnableThinking {
			t.Errorf("CLI did not apply provider configuration: %+v", body)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"done\":true}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	t.Setenv("ASTERIA_MODEL_PROVIDER", "qwen")
	t.Setenv("ASTERIA_MODEL_URL", server.URL+"/custom")
	t.Setenv("ASTERIA_MODEL_NAME", "qwen-test")
	t.Setenv("ASTERIA_MODEL_KEY", "test-key")
	t.Setenv("ASTERIA_MODEL_MAX_TOKENS", "2048")
	t.Setenv("ASTERIA_MODEL_TIMEOUT", "2s")
	data := filepath.Join(t.TempDir(), "data")
	run := func(args ...string) ([]byte, error) {
		var out bytes.Buffer
		command := New(strings.NewReader(""), &out, io.Discard)
		command.SetArgs(append([]string{"--data", data}, args...))
		err := command.Execute()
		return out.Bytes(), err
	}
	if _, err := run("--offline", "init", "--schema", filepath.Join("..", "..", "examples", "traffic.yaml")); err != nil {
		t.Fatal(err)
	}
	result, err := run("query", "停车", "--scope", "traffic")
	if err != nil {
		t.Fatal(err)
	}
	var query core.QueryResult
	if err := json.Unmarshal(result, &query); err != nil {
		t.Fatal(err)
	}
	if query.Steps != 1 || query.StopReason != "sufficient_evidence" {
		t.Fatalf("CLI did not use the model: %s", result)
	}
	t.Setenv("ASTERIA_MODEL_PROVIDER", "invalid")
	t.Setenv("ASTERIA_MODEL_MAX_TOKENS", "invalid")
	if _, err := run("--offline", "libraries"); err != nil {
		t.Fatalf("offline did not bypass invalid model settings: %v", err)
	}
	if _, err := run("libraries"); err == nil {
		t.Fatal("invalid model settings were silently ignored")
	}
}
