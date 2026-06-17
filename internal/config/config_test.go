package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Valid(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"SPOTIFY_CLIENT_ID":     "id",
		"SPOTIFY_CLIENT_SECRET": "secret",
		"PORT":                  "9090",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SpotifyClientID != "id" || cfg.SpotifyClientSecret != "secret" {
		t.Errorf("credentials not loaded: %+v", cfg)
	}
	if cfg.Addr != ":9090" {
		t.Errorf("Addr = %q, want :9090", cfg.Addr)
	}
}

func TestLoad_DatabaseURL(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"SPOTIFY_CLIENT_ID":     "id",
		"SPOTIFY_CLIENT_SECRET": "secret",
		"DATABASE_URL":          "postgres://u@localhost/db",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseURL != "postgres://u@localhost/db" {
		t.Errorf("DatabaseURL = %q, not loaded", cfg.DatabaseURL)
	}
}

func TestLoad_DatabaseURLOptional(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"SPOTIFY_CLIENT_ID":     "id",
		"SPOTIFY_CLIENT_SECRET": "secret",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("DatabaseURL = %q, want empty when unset", cfg.DatabaseURL)
	}
}

func TestLoad_DefaultsPort(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"SPOTIFY_CLIENT_ID":     "id",
		"SPOTIFY_CLIENT_SECRET": "secret",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want default :8080", cfg.Addr)
	}
}

func TestLoad_MissingClientID(t *testing.T) {
	_, err := Load(env(map[string]string{"SPOTIFY_CLIENT_SECRET": "secret"}))
	if err == nil {
		t.Fatal("expected error for missing client ID")
	}
	if !strings.Contains(err.Error(), "SPOTIFY_CLIENT_ID") {
		t.Errorf("error %q should name the missing var", err)
	}
}

func TestLoad_MissingClientSecret(t *testing.T) {
	_, err := Load(env(map[string]string{"SPOTIFY_CLIENT_ID": "id"}))
	if err == nil {
		t.Fatal("expected error for missing client secret")
	}
	if !strings.Contains(err.Error(), "SPOTIFY_CLIENT_SECRET") {
		t.Errorf("error %q should name the missing var", err)
	}
}
