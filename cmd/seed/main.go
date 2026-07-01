// Command seed populates the mix-match corpus with a curated list of tracks.
// It runs the full ingest pipeline (Spotify lookup → ReccoBeats features →
// Camelot derivation → Postgres upsert), so it requires the same env vars as
// the server: SPOTIFY_CLIENT_ID, SPOTIFY_CLIENT_SECRET, and DATABASE_URL.
//
// Usage:
//
//	DATABASE_URL=... SPOTIFY_CLIENT_ID=... SPOTIFY_CLIENT_SECRET=... go run ./cmd/seed
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aaronpollock/liner-notes-server/internal/config"
	"github.com/aaronpollock/liner-notes-server/internal/ingest"
	"github.com/aaronpollock/liner-notes-server/internal/lookup"
	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
	"github.com/aaronpollock/liner-notes-server/internal/spotify"
	"github.com/aaronpollock/liner-notes-server/internal/spotifyauth"
	"github.com/aaronpollock/liner-notes-server/internal/store"
)

// seeds is the curated list of tracks to load into the corpus.
// Add entries here before running; Title and Artist must match the record label
// source (not Spotify). ISRC is optional but improves lookup accuracy.
var seeds = []ingest.Item{
	{Title: "Daftendirekt", Artist: "Daft Punk", Source: "seed"},
	{Title: "WDPK 83.7 FM", Artist: "Daft Punk", Source: "seed"},
	{Title: "Revolution 909", Artist: "Daft Punk", Source: "seed"},
	{Title: "Da Funk", Artist: "Daft Punk", Source: "seed"},
	{Title: "Phoenix", Artist: "Daft Punk", Source: "seed"},
	{Title: "Fresh", Artist: "Daft Punk", Source: "seed"},
	{Title: "Around the World", Artist: "Daft Punk", Source: "seed"},
	{Title: "Rollin' & Scratchin'", Artist: "Daft Punk", Source: "seed"},
	{Title: "Teachers", Artist: "Daft Punk", Source: "seed"},
	{Title: "High Fidelity", Artist: "Daft Punk", Source: "seed"},
	{Title: "Rock'n Roll", Artist: "Daft Punk", Source: "seed"},
	{Title: "Oh Yeah", Artist: "Daft Punk", Source: "seed"},
	{Title: "Burnin'", Artist: "Daft Punk", Source: "seed"},
	{Title: "Indo Silver Club", Artist: "Daft Punk", Source: "seed"},
	{Title: "Alive", Artist: "Daft Punk", Source: "seed"},
	{Title: "Funk Ad", Artist: "Daft Punk", Source: "seed"},
	{Title: "Brighter", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Like An Animal", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Say a Prayer for Me", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "You Were Right", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Be with You", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Daylight", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Hypnotised", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Tell Me", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Until the Sun Needs to Rise", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Lose My Head", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Innerbloom", Artist: "RÜFÜS DU SOL", Source: "seed"},
	{Title: "Days Gone By", Artist: "Bob Moses", Source: "seed"},
	{Title: "Like It or Not", Artist: "Bob Moses", Source: "seed"},
	{Title: "Talk", Artist: "Bob Moses", Source: "seed"},
	{Title: "Before I Lose My Mind", Artist: "Bob Moses", Source: "seed"},
	{Title: "Keeping Me Alive", Artist: "Bob Moses", Source: "seed"},
	{Title: "Nothing At All", Artist: "Bob Moses", Source: "seed"},
	{Title: "Tearing Me Up", Artist: "Bob Moses", Source: "seed"},
	{Title: "Writing on the Wall", Artist: "Bob Moses", Source: "seed"},
	{Title: "All I Want", Artist: "Bob Moses", Source: "seed"},
	{Title: "Touch and Go", Artist: "Bob Moses", Source: "seed"},
	{Title: "La femme d'argent", Artist: "Air", Source: "seed"},
	{Title: "Sexy Boy", Artist: "Air", Source: "seed"},
	{Title: "All I Need", Artist: "Air", Source: "seed"},
	{Title: "Kelly Watch the Stars", Artist: "Air", Source: "seed"},
	{Title: "Talisman", Artist: "Air", Source: "seed"},
	{Title: "Remember", Artist: "Air", Source: "seed"},
	{Title: "You Make It Easy", Artist: "Air", Source: "seed"},
	{Title: "Ce matin-là", Artist: "Air", Source: "seed"},
	{Title: "New Star in the Sky", Artist: "Air", Source: "seed"},
	{Title: "Le voyage de Pénélope", Artist: "Air", Source: "seed"},
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required for seeding")
	}
	if len(seeds) == 0 {
		logger.Info("no seed tracks defined — add entries to the seeds slice in cmd/seed/main.go")
		return nil
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}

	tokens := spotifyauth.NewClientCredentials(
		cfg.SpotifyClientID, cfg.SpotifyClientSecret,
		spotifyauth.WithHTTPClient(httpClient),
	)
	search := spotify.NewClient(tokens,
		spotify.WithHTTPClient(httpClient),
		spotify.WithLogger(logger),
	)
	features := reccobeats.NewClient(
		reccobeats.WithHTTPClient(httpClient),
		reccobeats.WithLogger(logger),
	)

	ctx := context.Background()

	st, err := store.Open(ctx, cfg.DatabaseURL, store.WithLogger(logger))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	svc := lookup.NewService(search, features, st)
	ing := ingest.New(svc, st, ingest.WithLogger(logger))

	logger.Info("seeding corpus", "tracks", len(seeds))
	summary := ing.IngestList(ctx, seeds)
	logger.Info("done",
		"ingested", summary.Ingested,
		"unresolved", summary.Unresolved,
		"no_features", summary.NoFeatures,
		"errored", summary.Errored,
	)
	return nil
}
