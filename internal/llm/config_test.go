package llm

import (
	"testing"
	"time"
)

func TestModelConfiguration(t *testing.T) {
	for name, edit := range map[string]func(*Config){
		"unknown provider":       func(c *Config) { c.Provider = "unknown" },
		"missing model":          func(c *Config) { c.Model = " " },
		"missing named key":      func(c *Config) { c.Provider = "glm" },
		"missing compatible URL": func(c *Config) { c.BaseURL = "" },
		"invalid URL":            func(c *Config) { c.BaseURL = "file:///tmp/model" },
		"URL credentials":        func(c *Config) { c.BaseURL = "https://user:secret@example.com/v1" },
		"URL query":              func(c *Config) { c.BaseURL += "?key=secret" },
		"URL fragment":           func(c *Config) { c.BaseURL += "#fragment" },
		"negative tokens":        func(c *Config) { c.MaxTokens = -1 },
		"negative timeout":       func(c *Config) { c.Timeout = -time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			config := Config{BaseURL: "http://localhost:11434/v1", Model: "local"}
			edit(&config)
			if _, err := New(config); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
	for _, provider := range []string{"", "compatible", "openrouter", "deepseek", "qwen", "kimi", "glm"} {
		for _, path := range []string{"/regional/v1", "/regional/v1/", "/regional/v1/chat/completions/"} {
			client, err := New(Config{Provider: provider, BaseURL: "https://proxy.example.com" + path, Model: "custom-model", APIKey: "key"})
			if err != nil {
				t.Fatal(err)
			}
			if client.endpoint != "https://proxy.example.com/regional/v1/chat/completions" || client.client.Timeout != 90*time.Second {
				t.Fatalf("URL override or default timeout was lost: %+v", client)
			}
		}
	}
}
