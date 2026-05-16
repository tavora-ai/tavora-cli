package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// configFile holds the parsed config from ~/.tavora.yaml
type configFile struct {
	APIKey string `yaml:"api_key"`
	URL    string `yaml:"url"`
}

var configPath string

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tavora.yaml")
}

// loadConfigFile reads the config file if it exists.
func loadConfigFile() *configFile {
	path := configPath
	if path == "" {
		path = os.Getenv("TAVORA_CONFIG")
	}
	if path == "" {
		path = defaultConfigPath()
	}
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

// loginCmd was previously bound to `tavora init`; renamed when the
// code-first verbs took over `init` for project scaffolding. The
// behavior — interactively write ~/.tavora.yaml with API key + URL —
// is unchanged.
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Interactive credential setup — configure API key and server URL",
	RunE: func(cmd *cobra.Command, args []string) error {
		reader := bufio.NewReader(os.Stdin)

		path := defaultConfigPath()
		if path == "" {
			return fmt.Errorf("cannot determine home directory")
		}

		// Load existing config for defaults
		existing := loadConfigFile()

		// Prompt for URL
		defaultURL := "http://localhost:8080"
		if existing != nil && existing.URL != "" {
			defaultURL = existing.URL
		}
		fmt.Printf("Server URL [%s]: ", defaultURL)
		urlInput, _ := reader.ReadString('\n')
		urlInput = strings.TrimSpace(urlInput)
		if urlInput == "" {
			urlInput = defaultURL
		}

		// Prompt for API key
		defaultKey := ""
		hint := ""
		if existing != nil && existing.APIKey != "" {
			// Show masked version of existing key
			defaultKey = existing.APIKey
			if len(defaultKey) > 8 {
				hint = defaultKey[:8] + "..."
			} else {
				hint = defaultKey
			}
		}
		if hint != "" {
			fmt.Printf("API Key [%s]: ", hint)
		} else {
			fmt.Print("API Key: ")
		}
		keyInput, _ := reader.ReadString('\n')
		keyInput = strings.TrimSpace(keyInput)
		if keyInput == "" {
			keyInput = defaultKey
		}

		if keyInput == "" {
			return fmt.Errorf("API key is required — create one with tavora-admin api-keys create")
		}

		cfg := configFile{
			APIKey: keyInput,
			URL:    urlInput,
		}

		data, err := yaml.Marshal(&cfg)
		if err != nil {
			return fmt.Errorf("failed to serialize config: %w", err)
		}

		if err := os.WriteFile(path, data, 0600); err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}

		fmt.Printf("\nConfig saved to %s\n", path)
		fmt.Printf("  URL:     %s\n", cfg.URL)
		fmt.Printf("  API Key: %s...\n", cfg.APIKey[:min(8, len(cfg.APIKey))])
		return nil
	},
}
