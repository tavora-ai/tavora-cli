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

// maskKey returns a short prefix-only preview of an API key for
// display in prompts and the post-save confirmation.
func maskKey(k string) string {
	if len(k) > 8 {
		return k[:8] + "..."
	}
	return k
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

		// Env vars take precedence over the existing config file when
		// pre-filling defaults — if the user has TAVORA_API_KEY /
		// TAVORA_URL exported, they almost certainly want `tavora
		// login` to capture those into the config, not override them
		// with a stale ~/.tavora.yaml.
		envURL := strings.TrimSpace(os.Getenv("TAVORA_URL"))
		envKey := strings.TrimSpace(os.Getenv("TAVORA_API_KEY"))

		// Prompt for URL
		defaultURL := "http://localhost:8080"
		urlSource := ""
		if existing != nil && existing.URL != "" {
			defaultURL = existing.URL
		}
		if envURL != "" {
			defaultURL = envURL
			urlSource = " (from $TAVORA_URL)"
		}
		fmt.Printf("Server URL [%s]%s: ", defaultURL, urlSource)
		urlInput, _ := reader.ReadString('\n')
		urlInput = strings.TrimSpace(urlInput)
		if urlInput == "" {
			urlInput = defaultURL
		}

		// Prompt for API key. Print a deep link so the user can
		// jump directly to the api-keys page in the dashboard
		// without manual navigation — Convex-style `convex login`
		// uses a full browser-OAuth dance; this is the cheap
		// halfway-house version that at least skips the "where do I
		// find this?" hop.
		defaultKey := ""
		hint := ""
		keySource := ""
		if existing != nil && existing.APIKey != "" {
			defaultKey = existing.APIKey
			hint = maskKey(defaultKey)
		}
		if envKey != "" {
			defaultKey = envKey
			hint = maskKey(defaultKey)
			keySource = " (from $TAVORA_API_KEY)"
		}
		if defaultKey == "" {
			fmt.Printf("\nNeed a key? Open %s/settings/api-keys and create one.\n\n", strings.TrimRight(urlInput, "/"))
		}
		if hint != "" {
			fmt.Printf("API Key [%s]%s: ", hint, keySource)
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
		fmt.Printf("  API Key: %s\n", maskKey(cfg.APIKey))
		return nil
	},
}
