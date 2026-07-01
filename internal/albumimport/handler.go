// Package albumimport provides the POST /v1/album/import endpoint, which
// fetches all tracks for a named album from Spotify and runs them through the
// ingest pipeline to populate the mix-match corpus.
package albumimport

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/aaronpollock/liner-notes-server/internal/ingest"
	"github.com/aaronpollock/liner-notes-server/internal/spotify"
)

// AlbumFetcher retrieves the track listing for a named album. *spotify.Client
// satisfies this interface.
type AlbumFetcher interface {
	AlbumTracks(ctx context.Context, albumName, artistName string) ([]spotify.Track, error)
}

// Ingester runs the ingest pipeline over a list of items. *ingest.Ingester
// satisfies this interface.
type Ingester interface {
	IngestList(ctx context.Context, items []ingest.Item) ingest.Summary
}

// Handler serves POST /v1/album/import.
type Handler struct {
	fetcher  AlbumFetcher
	ingester Ingester
	logger   *slog.Logger
}

// NewHandler builds a Handler.
func NewHandler(fetcher AlbumFetcher, ingester Ingester, opts ...Option) *Handler {
	h := &Handler{fetcher: fetcher, ingester: ingester, logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Option configures a Handler.
type Option func(*Handler)

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option {
	return func(h *Handler) { h.logger = l }
}

type importRequest struct {
	Artist string `json:"artist"`
	Album  string `json:"album"`
}

type importResponse struct {
	Ingested   int `json:"ingested"`
	Unresolved int `json:"unresolved"`
	NoFeatures int `json:"no_features"`
	Errored    int `json:"errored"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req importRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Artist == "" || req.Album == "" {
		http.Error(w, "artist and album are required", http.StatusBadRequest)
		return
	}

	tracks, err := h.fetcher.AlbumTracks(r.Context(), req.Album, req.Artist)
	if err != nil {
		h.logger.Error("album import: fetch tracks failed", "artist", req.Artist, "album", req.Album, "err", err)
		http.Error(w, "failed to fetch album tracks from Spotify", http.StatusInternalServerError)
		return
	}

	items := make([]ingest.Item, 0, len(tracks))
	for _, t := range tracks {
		artist := req.Artist
		if len(t.Artists) > 0 {
			artist = t.Artists[0]
		}
		items = append(items, ingest.Item{
			Title:  t.Name,
			Artist: artist,
			Source: "album_import",
		})
	}

	summary := h.ingester.IngestList(r.Context(), items)
	h.logger.Info("album import complete",
		"artist", req.Artist,
		"album", req.Album,
		"tracks", len(items),
		"ingested", summary.Ingested,
		"unresolved", summary.Unresolved,
		"no_features", summary.NoFeatures,
		"errored", summary.Errored,
	)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(importResponse{
		Ingested:   summary.Ingested,
		Unresolved: summary.Unresolved,
		NoFeatures: summary.NoFeatures,
		Errored:    summary.Errored,
	})
}
