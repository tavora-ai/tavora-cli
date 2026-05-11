package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds the persisted credentials the TUI needs to connect to a
// Tavora app. The API key is app-scoped — there is nothing
// else to "select" once it is supplied.
type Config struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

func configPath() (string, error) {
	if v := os.Getenv("TAVORA_TUI_CONFIG"); v != "" {
		return v, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config dir: %w", err)
	}
	return filepath.Join(dir, "tavora", "agent-tui.json"), nil
}

// LoadConfig returns the persisted config, or os.ErrNotExist when the
// user has not finished setup yet.
func LoadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if cfg.URL == "" || cfg.APIKey == "" {
		return nil, errors.New("config file is missing url or api_key")
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// EnvConfig returns a Config populated from TAVORA_URL / TAVORA_API_KEY
// if both are set, otherwise nil. Env vars take precedence over the
// stored file so CI runs and one-off invocations stay declarative.
func EnvConfig() *Config {
	url := os.Getenv("TAVORA_URL")
	key := os.Getenv("TAVORA_API_KEY")
	if url == "" || key == "" {
		return nil
	}
	return &Config{URL: url, APIKey: key}
}
