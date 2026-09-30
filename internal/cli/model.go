package cli

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"asteria/internal/core"
	"asteria/internal/llm"
)

func modelFromEnv() (core.Model, error) {
	config := llm.Config{
		Provider: os.Getenv("ASTERIA_MODEL_PROVIDER"),
		BaseURL:  os.Getenv("ASTERIA_MODEL_URL"),
		Model:    os.Getenv("ASTERIA_MODEL_NAME"),
		APIKey:   os.Getenv("ASTERIA_MODEL_KEY"),
	}
	tokens := os.Getenv("ASTERIA_MODEL_MAX_TOKENS")
	timeout := os.Getenv("ASTERIA_MODEL_TIMEOUT")
	if config.Provider == "" && config.BaseURL == "" && config.Model == "" && config.APIKey == "" && tokens == "" && timeout == "" {
		return nil, nil
	}
	var err error
	if tokens != "" {
		config.MaxTokens, err = strconv.Atoi(tokens)
		if err != nil || config.MaxTokens <= 0 {
			return nil, fmt.Errorf("ASTERIA_MODEL_MAX_TOKENS must be a positive integer")
		}
	}
	if timeout != "" {
		config.Timeout, err = time.ParseDuration(timeout)
		if err != nil || config.Timeout <= 0 {
			return nil, fmt.Errorf("ASTERIA_MODEL_TIMEOUT must be a positive duration, for example 90s")
		}
	}
	model, err := llm.New(config)
	if err != nil {
		return nil, err
	}
	return model, nil
}
