// Package ingest populates the mix-match corpus. The per-track core
// (IngestOne) resolves a track via the lookup pipeline, derives its Camelot
// code, and persists it; IngestList is the track-list entry point over that
// core. A future record/album flow can reuse IngestOne directly.
package ingest

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aaronpollock/liner-notes-server/internal/camelot"
	"github.com/aaronpollock/liner-notes-server/internal/lookup"
	"github.com/aaronpollock/liner-notes-server/internal/store"
)

// Item is one track to ingest. Title and artist are required; ISRC is optional.
// Title/artist must come from the ingestion source (not Spotify), per the terms.
type Item struct {
	Title  string
	Artist string
	ISRC   string
	Source string
}

// Outcome reports what happened to a single item.
type Outcome string

const (
	Ingested   Outcome = "ingested"    // stored with mixing features
	Unresolved Outcome = "unresolved"  // no Spotify match
	NoFeatures Outcome = "no_features" // resolved, but no usable ReccoBeats features
)

// Looker resolves a track to a Spotify ID and audio features. *lookup.Service
// satisfies it.
type Looker interface {
	Lookup(ctx context.Context, req lookup.Request) (lookup.Result, error)
}

// Corpus persists resolved tracks and their mixing features. *store.Store
// satisfies it.
type Corpus interface {
	UpsertTrack(ctx context.Context, t store.Track) (string, error)
	UpsertMixFeatures(ctx context.Context, spotifyID string, f store.MixFeatures) error
}

// Ingester ties the lookup pipeline to the corpus store.
type Ingester struct {
	looker Looker
	corpus Corpus
	logger *slog.Logger
}

// Option configures an Ingester.
type Option func(*Ingester)

// WithLogger sets the logger used to record skipped (unresolved / featureless) items.
func WithLogger(l *slog.Logger) Option {
	return func(i *Ingester) { i.logger = l }
}

// New builds an Ingester.
func New(looker Looker, corpus Corpus, opts ...Option) *Ingester {
	i := &Ingester{looker: looker, corpus: corpus, logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(i)
	}
	return i
}

// IngestOne resolves and persists a single track. It returns a normal Outcome
// for tracks that can't be resolved or have no usable features (recorded, not
// stored); it returns an error only for invalid input or a store failure.
func (i *Ingester) IngestOne(ctx context.Context, item Item) (Outcome, error) {
	res, err := i.looker.Lookup(ctx, lookup.Request{ISRC: item.ISRC, Title: item.Title, Artist: item.Artist})
	if err != nil {
		return "", fmt.Errorf("ingest %q/%q: %w", item.Title, item.Artist, err)
	}

	if res.SpotifyID == "" {
		i.logger.Info("ingest: unresolved", "title", item.Title, "artist", item.Artist)
		return Unresolved, nil
	}
	if res.FeaturesStatus != lookup.StatusAvailable || res.Features == nil {
		i.logger.Info("ingest: no features", "title", item.Title, "artist", item.Artist, "spotify_id", res.SpotifyID)
		return NoFeatures, nil
	}
	f := res.Features
	// A track whose key can't be mapped to a Camelot code is useless for
	// harmonic matching; skip it rather than store a track with no features.
	if _, err := camelot.Code(f.Key, f.Mode); err != nil {
		i.logger.Info("ingest: unmappable key", "title", item.Title, "artist", item.Artist, "key", f.Key, "mode", f.Mode)
		return NoFeatures, nil
	}

	if _, err := i.corpus.UpsertTrack(ctx, store.Track{
		SpotifyID: res.SpotifyID,
		ISRC:      item.ISRC,
		Title:     item.Title,
		Artist:    item.Artist,
		Source:    item.Source,
	}); err != nil {
		return "", fmt.Errorf("ingest upsert track %q: %w", res.SpotifyID, err)
	}
	if err := i.corpus.UpsertMixFeatures(ctx, res.SpotifyID, store.MixFeatures{
		Tempo:    f.Tempo,
		Key:      f.Key,
		Mode:     f.Mode,
		Loudness: f.Loudness,
	}); err != nil {
		return "", fmt.Errorf("ingest upsert features %q: %w", res.SpotifyID, err)
	}
	return Ingested, nil
}

// Summary tallies the outcomes of an IngestList run.
type Summary struct {
	Ingested   int
	Unresolved int
	NoFeatures int
	Errored    int
}

// IngestList ingests every item, continuing past per-item failures and
// returning a tally. The whole batch never hard-fails on a single bad item.
func (i *Ingester) IngestList(ctx context.Context, items []Item) Summary {
	var s Summary
	for _, item := range items {
		out, err := i.IngestOne(ctx, item)
		if err != nil {
			s.Errored++
			i.logger.Error("ingest: item failed", "title", item.Title, "artist", item.Artist, "err", err.Error())
			continue
		}
		switch out {
		case Ingested:
			s.Ingested++
		case Unresolved:
			s.Unresolved++
		case NoFeatures:
			s.NoFeatures++
		}
	}
	return s
}
