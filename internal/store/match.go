package store

import (
	"context"
	"fmt"

	"github.com/aaronpollock/liner-notes-server/internal/camelot"
)

// defaultMatchLimit caps results when the caller passes a non-positive limit.
const defaultMatchLimit = 20

// MixSeed describes the track to find mixes for. SpotifyID is excluded from the
// results so a track never matches itself.
type MixSeed struct {
	SpotifyID string
	Tempo     float64
	Key       int
	Mode      int
	Loudness  float64
}

// Match is a corpus track that mixes well with the seed.
type Match struct {
	SpotifyID string
	Title     string
	Artist    string
	Camelot   string
	Tempo     float64
	Loudness  float64
}

// MixMatches returns corpus tracks that mix well with the seed: harmonically
// compatible by Camelot code, within ±5% tempo (including half- and
// double-time), ordered by loudness closeness (smoothest transition first). It
// returns camelot.ErrUnknownKey when the seed's key can't be mapped.
func (s *Store) MixMatches(ctx context.Context, seed MixSeed, limit int) ([]Match, error) {
	code, err := camelot.Code(seed.Key, seed.Mode)
	if err != nil {
		return nil, err
	}
	compatible := camelot.Compatible(code)
	if limit <= 0 {
		limit = defaultMatchLimit
	}

	// ±5% windows around the seed tempo and its half/double, so beatmatched
	// pairs at related tempos are included.
	const lo, hi = 0.95, 1.05
	rows, err := s.pool.Query(ctx,
		`select t.spotify_id, t.title, t.artist, mf.camelot, mf.tempo, mf.loudness
		 from mix_features mf
		 join tracks t on t.id = mf.track_id
		 where mf.camelot = any($1)
		   and ( mf.tempo between $2 and $3
		      or mf.tempo between $4 and $5
		      or mf.tempo between $6 and $7 )
		   and t.spotify_id <> $8
		 order by abs(mf.loudness - $9) asc
		 limit $10`,
		compatible,
		seed.Tempo*lo, seed.Tempo*hi,
		seed.Tempo*2*lo, seed.Tempo*2*hi,
		seed.Tempo/2*lo, seed.Tempo/2*hi,
		seed.SpotifyID, seed.Loudness, limit)
	if err != nil {
		return nil, fmt.Errorf("store: mix matches query: %w", err)
	}
	defer rows.Close()

	var matches []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.SpotifyID, &m.Title, &m.Artist, &m.Camelot, &m.Tempo, &m.Loudness); err != nil {
			return nil, fmt.Errorf("store: mix matches scan: %w", err)
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: mix matches rows: %w", err)
	}
	return matches, nil
}
