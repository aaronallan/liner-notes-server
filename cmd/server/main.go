// Command server runs the Liner Notes backend HTTP service: it resolves a
// posted ISRC into a track's audio characteristics via Spotify Search and
// ReccoBeats.
package main

import (
	"context"
	"errors"
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
	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
	"github.com/aaronpollock/liner-notes-server/internal/spotify"
	"github.com/aaronpollock/liner-notes-server/internal/spotifyauth"
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
	search := spotify.NewClient(tokens, spotify.WithHTTPClient(httpClient))
	features := reccobeats.NewClient(
		reccobeats.WithHTTPClient(httpClient),
		reccobeats.WithLogger(logger),
	)
	idCache := cache.NewMemory[string, string]()

	svc := lookup.NewService(search, features, idCache)

	mux := http.NewServeMux()
	mux.Handle("/v1/lookup", lookup.NewHandler(svc))
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
