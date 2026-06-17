// Package store is the Postgres-backed persistence layer: the lookup resolution
// cache (normalized title+artist -> Spotify ID) and the mix-match corpus
// (tracks plus their ReccoBeats-derived mixing features and Camelot code).
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aaronpollock/liner-notes-server/internal/camelot"
)

// cacheTimeout bounds the implicit-context cache operations, which run outside
// the request lifecycle (the IDCache interface carries no context).
const cacheTimeout = 2 * time.Second

// Store is a Postgres-backed persistence layer.
type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// Option configures a Store.
type Option func(*Store)

// WithLogger sets the logger used for cache-path errors (which are swallowed so
// a database hiccup degrades to a cache miss rather than failing a scan).
func WithLogger(l *slog.Logger) Option {
	return func(s *Store) { s.logger = l }
}

// Open connects to Postgres and verifies the connection.
func Open(ctx context.Context, dsn string, opts ...Option) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	s := &Store{pool: pool, logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(s)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return s, nil
}

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

// Get implements lookup.IDCache: it returns the cached Spotify ID for a
// normalized key. A database error is treated as a miss (logged), so resolution
// degrades to a fresh Spotify search rather than failing.
func (s *Store) Get(key string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()

	var id string
	err := s.pool.QueryRow(ctx, "select spotify_id from id_cache where norm_key = $1", key).Scan(&id)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			s.logger.Error("store: id_cache get", "err", err.Error())
		}
		return "", false
	}
	return id, true
}

// Set implements lookup.IDCache: it upserts the normalized key -> Spotify ID
// mapping. Errors are logged and swallowed (a failed write just means the next
// lookup re-searches).
func (s *Store) Set(key, spotifyID string) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()

	_, err := s.pool.Exec(ctx,
		`insert into id_cache (norm_key, spotify_id) values ($1, $2)
		 on conflict (norm_key) do update set spotify_id = excluded.spotify_id`,
		key, spotifyID)
	if err != nil {
		s.logger.Error("store: id_cache set", "err", err.Error())
	}
}

// Track is a corpus track's identity row. Title/Artist must be ingestion-sourced.
type Track struct {
	SpotifyID string
	ISRC      string // optional
	Title     string
	Artist    string
	Source    string // optional
}

// MixFeatures holds the ReccoBeats-derived mixing signals for a track. The
// Camelot code is derived from Key/Mode at write time.
type MixFeatures struct {
	Tempo    float64
	Key      int
	Mode     int
	Loudness float64
}

// UpsertTrack inserts or updates a corpus track by Spotify ID and returns its
// row ID. It is idempotent: re-upserting the same Spotify ID updates in place.
func (s *Store) UpsertTrack(ctx context.Context, t Track) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`insert into tracks (spotify_id, isrc, title, artist, source)
		 values ($1, $2, $3, $4, $5)
		 on conflict (spotify_id) do update set
		   isrc = excluded.isrc, title = excluded.title,
		   artist = excluded.artist, source = excluded.source
		 returning id`,
		t.SpotifyID, nullStr(t.ISRC), t.Title, t.Artist, nullStr(t.Source)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: upsert track: %w", err)
	}
	return id, nil
}

// UpsertMixFeatures inserts or updates the mixing features for an existing track
// (identified by Spotify ID), deriving and storing the Camelot code. It returns
// camelot.ErrUnknownKey for an unresolvable key, and an error if the track does
// not exist yet (UpsertTrack must run first).
func (s *Store) UpsertMixFeatures(ctx context.Context, spotifyID string, f MixFeatures) error {
	code, err := camelot.Code(f.Key, f.Mode)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		`insert into mix_features (track_id, tempo, key, mode, loudness, camelot)
		 select id, $2, $3, $4, $5, $6 from tracks where spotify_id = $1
		 on conflict (track_id) do update set
		   tempo = excluded.tempo, key = excluded.key, mode = excluded.mode,
		   loudness = excluded.loudness, camelot = excluded.camelot, fetched_at = now()`,
		spotifyID, f.Tempo, f.Key, f.Mode, f.Loudness, code)
	if err != nil {
		return fmt.Errorf("store: upsert mix features: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: no track with spotify_id %q", spotifyID)
	}
	return nil
}

// nullStr maps an empty string to a SQL NULL so optional columns stay null.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
