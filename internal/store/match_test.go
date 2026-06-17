package store

import (
	"context"
	"testing"
)

// seedTrack inserts a track + its mixing features (key/mode drive the Camelot
// code) so match tests can craft a corpus.
func seedTrack(t *testing.T, s *Store, spotifyID string, tempo float64, key, mode int, loudness float64) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.UpsertTrack(ctx, Track{SpotifyID: spotifyID, Title: spotifyID, Artist: "A"}); err != nil {
		t.Fatalf("seed track %s: %v", spotifyID, err)
	}
	if err := s.UpsertMixFeatures(ctx, spotifyID, MixFeatures{Tempo: tempo, Key: key, Mode: mode, Loudness: loudness}); err != nil {
		t.Fatalf("seed features %s: %v", spotifyID, err)
	}
}

func ids(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.SpotifyID
	}
	return out
}

// The seed for these tests is key=9, mode=0 -> Camelot 8A.
// Compatible codes: {8A, 7A, 9A, 8B}. Seed tempo 120 (window 114..126).
func seed8A(spotifyID string) MixSeed {
	return MixSeed{SpotifyID: spotifyID, Tempo: 120, Key: 9, Mode: 0, Loudness: -5}
}

func TestMixMatches_FiltersByKeyAndTempo(t *testing.T) {
	s := testStore(t)

	seedTrack(t, s, "compat-intempo", 122, 9, 0, -5)   // 8A, in window  -> match
	seedTrack(t, s, "compat-outtempo", 100, 9, 0, -5)  // 8A, out of window -> no
	seedTrack(t, s, "incompat-intempo", 122, 8, 0, -5) // 1A, in window -> no (key)
	seedTrack(t, s, "relative-major", 124, 0, 1, -5)   // 8B (relative), in window -> match

	got, err := s.MixMatches(context.Background(), seed8A("seed"), 10)
	if err != nil {
		t.Fatalf("MixMatches: %v", err)
	}
	want := map[string]bool{"compat-intempo": true, "relative-major": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly %v", ids(got), want)
	}
	for _, m := range got {
		if !want[m.SpotifyID] {
			t.Errorf("unexpected match %q (camelot %s, tempo %v)", m.SpotifyID, m.Camelot, m.Tempo)
		}
	}
}

func TestMixMatches_IncludesHalfAndDoubleTime(t *testing.T) {
	s := testStore(t)
	// Seed tempo 70: base 66.5..73.5, double 133..147, half 33.25..36.75.
	seed := MixSeed{SpotifyID: "seed", Tempo: 70, Key: 9, Mode: 0, Loudness: -5}

	seedTrack(t, s, "double", 140, 9, 0, -5)    // double-time, compatible
	seedTrack(t, s, "half", 35, 9, 0, -5)       // half-time, compatible
	seedTrack(t, s, "unrelated", 110, 9, 0, -5) // outside all windows

	got, err := s.MixMatches(context.Background(), seed, 10)
	if err != nil {
		t.Fatalf("MixMatches: %v", err)
	}
	found := map[string]bool{}
	for _, m := range got {
		found[m.SpotifyID] = true
	}
	if !found["double"] || !found["half"] {
		t.Errorf("half/double-time matches missing: got %v", ids(got))
	}
	if found["unrelated"] {
		t.Errorf("out-of-window track should not match: got %v", ids(got))
	}
}

func TestMixMatches_OrdersByLoudnessCloseness(t *testing.T) {
	s := testStore(t)
	// Seed loudness -4; all compatible + in tempo window.
	seedTrack(t, s, "far", 120, 9, 0, -10)   // |Δ| = 6
	seedTrack(t, s, "near", 120, 9, 0, -4.5) // |Δ| = 0.5
	seedTrack(t, s, "mid", 120, 9, 0, -6)    // |Δ| = 2

	got, err := s.MixMatches(context.Background(), MixSeed{SpotifyID: "seed", Tempo: 120, Key: 9, Mode: 0, Loudness: -4}, 10)
	if err != nil {
		t.Fatalf("MixMatches: %v", err)
	}
	want := []string{"near", "mid", "far"}
	if got2 := ids(got); !equal(got2, want) {
		t.Errorf("order = %v, want %v (ascending |Δloudness|)", got2, want)
	}
}

func TestMixMatches_ExcludesSeedAndRespectsLimit(t *testing.T) {
	s := testStore(t)
	seedTrack(t, s, "seed", 120, 9, 0, -5) // the seed itself is in the corpus
	seedTrack(t, s, "m1", 120, 9, 0, -5)
	seedTrack(t, s, "m2", 121, 9, 0, -6)
	seedTrack(t, s, "m3", 119, 9, 0, -7)

	got, err := s.MixMatches(context.Background(), seed8A("seed"), 2)
	if err != nil {
		t.Fatalf("MixMatches: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (limit)", len(got))
	}
	for _, m := range got {
		if m.SpotifyID == "seed" {
			t.Errorf("seed track must be excluded from its own matches")
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
