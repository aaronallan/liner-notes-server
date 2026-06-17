// Command server runs the Liner Notes backend HTTP service: it resolves a
// posted ISRC into a track's audio characteristics via Spotify Search and
// ReccoBeats.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aaronpollock/liner-notes-server/internal/cache"
	"github.com/aaronpollock/liner-notes-server/internal/config"
	"github.com/aaronpollock/liner-notes-server/internal/lookup"
	"github.com/aaronpollock/liner-notes-server/internal/middleware"
	"github.com/aaronpollock/liner-notes-server/internal/mixmatch"
	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
	"github.com/aaronpollock/liner-notes-server/internal/spotify"
	"github.com/aaronpollock/liner-notes-server/internal/spotifyauth"
	"github.com/aaronpollock/liner-notes-server/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	// Shared HTTP client with a sane timeout for all upstream calls.
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

	// Use the Postgres store (durable cache + mix-match corpus) when a database
	// is configured; otherwise fall back to an in-memory cache for local dev.
	// The mix-match endpoint requires the corpus, so it is only served with a DB.
	var idCache lookup.IDCache
	var st *store.Store
	if cfg.DatabaseURL != "" {
		var err error
		st, err = store.Open(context.Background(), cfg.DatabaseURL, store.WithLogger(logger))
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.Migrate(context.Background()); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		logger.Info("using postgres store")
		idCache = st
	} else {
		logger.Info("using in-memory cache (no DATABASE_URL)")
		idCache = cache.NewMemory[string, string]()
	}

	svc := lookup.NewService(search, features, idCache)

	mux := http.NewServeMux()
	mux.Handle("/v1/lookup", lookup.NewHandler(svc))
	if st != nil {
		mux.Handle("/v1/mix-matches", mixmatch.NewHandler(svc, st))
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           middleware.Logging(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Run the server until an interrupt arrives, then shut down gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
