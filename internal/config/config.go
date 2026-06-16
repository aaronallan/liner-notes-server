// Package config loads runtime configuration from the environment. Secrets are
// never hardcoded; they are sourced from environment variables (or a secret
// manager that exports them) at startup.
package config

import "fmt"

const defaultPort = "8080"

// Config holds the backend's runtime settings.
type Config struct {
	SpotifyClientID     string
	SpotifyClientSecret string
	// Addr is the listen address for the HTTP server, e.g. ":8080".
	Addr string
}

// Load reads configuration using the given lookup function (typically os.Getenv).
// The Spotify credentials are required; the port defaults to 8080.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		SpotifyClientID:     getenv("SPOTIFY_CLIENT_ID"),
		SpotifyClientSecret: getenv("SPOTIFY_CLIENT_SECRET"),
	}
	if cfg.SpotifyClientID == "" {
		return Config{}, fmt.Errorf("config: SPOTIFY_CLIENT_ID is required")
	}
	if cfg.SpotifyClientSecret == "" {
		return Config{}, fmt.Errorf("config: SPOTIFY_CLIENT_SECRET is required")
	}

	port := getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	cfg.Addr = ":" + port
	return cfg, nil
}
