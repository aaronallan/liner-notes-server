package lookup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
)

// Looker is the behaviour the HTTP handler needs. *Service satisfies it; the
// interface keeps the handler testable without the full pipeline.
type Looker interface {
	Lookup(ctx context.Context, req Request) (Result, error)
}

// Handler serves the lookup endpoint: it accepts a POSTed ISRC (plus optional
// Shazam title/artist) and returns the track's characteristics as JSON.
type Handler struct {
	looker Looker
}

// NewHandler builds an http.Handler backed by the given Looker.
func NewHandler(looker Looker) *Handler {
	return &Handler{looker: looker}
}

// requestBody is the wire format accepted from the mobile client.
type requestBody struct {
	ISRC   string `json:"isrc"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

// responseBody is the wire format returned to the mobile client. Features is
// omitted (null) when audio features could not be resolved.
type responseBody struct {
	ISRC           string                    `json:"isrc"`
	Title          string                    `json:"title"`
	Artist         string                    `json:"artist"`
	SpotifyID      string                    `json:"spotify_id,omitempty"`
	Features       *reccobeats.AudioFeatures `json:"features"`
	FeaturesStatus FeaturesStatus            `json:"features_status"`
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

	result, err := h.looker.Lookup(r.Context(), Request{
		ISRC:   body.ISRC,
		Title:  body.Title,
		Artist: body.Artist,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			writeError(w, http.StatusBadRequest, "an isrc or title and artist is required")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}

	writeJSON(w, http.StatusOK, responseBody{
		ISRC:           result.ISRC,
		Title:          result.Title,
		Artist:         result.Artist,
		SpotifyID:      result.SpotifyID,
		Features:       result.Features,
		FeaturesStatus: result.FeaturesStatus,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
