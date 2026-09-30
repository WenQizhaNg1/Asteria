package llm

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config selects a provider without exposing its protocol to the core.
// Model is deliberately required: model availability changes independently of Asteria.
type Config struct {
	Provider  string
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int
	Timeout   time.Duration
}

func New(config Config) (*Chat, error) {
	config.Provider = strings.ToLower(strings.TrimSpace(config.Provider))
	if config.Provider == "" {
		config.Provider = "compatible"
	}
	var defaultURL string
	switch config.Provider {
	case "openrouter":
		defaultURL = "https://openrouter.ai/api/v1"
	case "deepseek":
		defaultURL = "https://api.deepseek.com"
	case "qwen":
		defaultURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case "kimi":
		defaultURL = "https://api.moonshot.cn/v1"
	case "glm":
		defaultURL = "https://open.bigmodel.cn/api/paas/v4"
	case "compatible":
	default:
		return nil, fmt.Errorf("model provider must be openrouter, deepseek, qwen, kimi, glm or compatible")
	}
	config.Model = strings.TrimSpace(config.Model)
	if config.Model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	config.APIKey = strings.TrimSpace(config.APIKey)
	if config.Provider != "compatible" && config.APIKey == "" {
		return nil, fmt.Errorf("%s model API key is required", config.Provider)
	}
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	if config.BaseURL == "" {
		config.BaseURL = defaultURL
	}
	u, err := url.Parse(config.BaseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("model URL must be http(s), without user info, query or fragment; compatible requires an explicit URL")
	}
	if config.MaxTokens < 0 || config.Timeout < 0 {
		return nil, fmt.Errorf("model token limit and timeout must be positive")
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 4096
	}
	if config.Timeout == 0 {
		config.Timeout = 90 * time.Second
	}
	endpoint := strings.TrimRight(config.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	return &Chat{config: config, endpoint: endpoint, client: &http.Client{Timeout: config.Timeout}}, nil
}
