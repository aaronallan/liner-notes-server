// Package mixmatch serves the mix-match endpoint: given a scanned track, it
// resolves the track's mixing features via the lookup pipeline and returns
// corpus tracks that mix well with it (harmonic key, ±5% tempo incl. half/double
// time, ranked by loudness closeness).
package mixmatch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/aaronpollock/liner-notes-server/internal/camelot"
	"github.com/aaronpollock/liner-notes-server/internal/lookup"
	"github.com/aaronpollock/liner-notes-server/internal/store"
)

// maxLimit caps how many matches a caller can request.
const maxLimit = 50

// Looker resolves a scanned track to a Spotify ID + audio features.
type Looker interface {
	Lookup(ctx context.Context, req lookup.Request) (lookup.Result, error)
}

// Matcher returns corpus tracks that mix well with a seed. *store.Store satisfies it.
type Matcher interface {
	MixMatches(ctx context.Context, seed store.MixSeed, limit int) ([]store.Match, error)
}

// Handler serves POST /v1/mix-matches.
type Handler struct {
	looker  Looker
	matcher Matcher
}

// NewHandler builds the mix-match handler.
func NewHandler(looker Looker, matcher Matcher) *Handler {
	return &Handler{looker: looker, matcher: matcher}
}

type requestBody struct {
	ISRC   string `json:"isrc"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Limit  int    `json:"limit"`
}

type matchJSON struct {
	SpotifyID string  `json:"spotify_id"`
	Title     string  `json:"title"`
	Artist    string  `json:"artist"`
	Camelot   string  `json:"camelot"`
	Tempo     float64 `json:"tempo"`
	Loudness  float64 `json:"loudness"`
}

type responseBody struct {
	SpotifyID string      `json:"spotify_id,omitempty"`
	Matches   []matchJSON `json:"matches"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body requestBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Resolve the seed's mixing features via the same pipeline as /v1/lookup.
	res, err := h.looker.Lookup(r.Context(), lookup.Request{ISRC: body.ISRC, Title: body.Title, Artist: body.Artist})
	if err != nil {
		if errors.Is(err, lookup.ErrInvalidRequest) {
			writeError(w, http.StatusBadRequest, "title and artist are required")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}

	// Without a resolved track and audio features we can't build a seed; return
	// an empty (but successful) result so the client degrades gracefully.
	if res.SpotifyID == "" || res.FeaturesStatus != lookup.StatusAvailable || res.Features == nil {
		writeJSON(w, http.StatusOK, responseBody{SpotifyID: res.SpotifyID, Matches: []matchJSON{}})
		return
	}

	seed := store.MixSeed{
		SpotifyID: res.SpotifyID,
		Tempo:     res.Features.Tempo,
		Key:       res.Features.Key,
		Mode:      res.Features.Mode,
		Loudness:  res.Features.Loudness,
	}
	matches, err := h.matcher.MixMatches(r.Context(), seed, limit(body.Limit))
	if err != nil {
		// An unmappable seed key just means no harmonic matches, not a failure.
		if errors.Is(err, camelot.ErrUnknownKey) {
			writeJSON(w, http.StatusOK, responseBody{SpotifyID: res.SpotifyID, Matches: []matchJSON{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "match failed")
		return
	}

	writeJSON(w, http.StatusOK, responseBody{SpotifyID: res.SpotifyID, Matches: toJSON(matches)})
}

func limit(requested int) int {
	if requested <= 0 {
		return 0 // let the store apply its default
	}
	if requested > maxLimit {
		return maxLimit
	}
	return requested
}

func toJSON(matches []store.Match) []matchJSON {
	out := make([]matchJSON, 0, len(matches))
	for _, m := range matches {
		out = append(out, matchJSON{
			SpotifyID: m.SpotifyID,
			Title:     m.Title,
			Artist:    m.Artist,
			Camelot:   m.Camelot,
			Tempo:     m.Tempo,
			Loudness:  m.Loudness,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
