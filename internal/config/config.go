package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

const defaultDatabasePath = "rice.db"

// Config contains the runtime configuration required by Rice.
type Config struct {
	DiscordToken  string
	GGDealsAPIKey string
	TestGuildID   string
	DatabasePath  string
}

// Load reads an optional local .env file, then resolves configuration from the
// process environment. Existing environment variables always take precedence.
func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	cfg := Config{
		DiscordToken:  strings.TrimSpace(os.Getenv("DISCORD_TOKEN")),
		GGDealsAPIKey: strings.TrimSpace(os.Getenv("GGDEALS_API_KEY")),
		TestGuildID:   strings.TrimSpace(os.Getenv("TEST_GUILD_ID")),
		DatabasePath:  strings.TrimSpace(os.Getenv("DATABASE_PATH")),
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = defaultDatabasePath
	}

	var missing []string
	if cfg.DiscordToken == "" {
		missing = append(missing, "DISCORD_TOKEN")
	}
	if cfg.GGDealsAPIKey == "" {
		missing = append(missing, "GGDEALS_API_KEY")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}
