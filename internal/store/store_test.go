package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aaronpollock/liner-notes-server/internal/camelot"
	"github.com/aaronpollock/liner-notes-server/internal/lookup"
)

// Store must satisfy the lookup cache interface.
var _ lookup.IDCache = (*Store)(nil)

// testStore opens a migrated, empty store against TEST_DATABASE_URL. Tests are
// skipped when it is unset so the suite still runs without a Postgres.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration tests")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := s.pool.Exec(ctx, "truncate id_cache, mix_features, tracks restart identity cascade"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMigrate_AppliesAndIsIdempotent(t *testing.T) {
	s := testStore(t)
	// Re-running migrations must be a no-op, not an error.
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	for _, tbl := range []string{"id_cache", "tracks", "mix_features"} {
		var reg *string
		if err := s.pool.QueryRow(context.Background(), "select to_regclass($1)", tbl).Scan(&reg); err != nil {
			t.Fatalf("to_regclass(%s): %v", tbl, err)
		}
		if reg == nil {
			t.Errorf("table %q missing after migrate", tbl)
		}
	}
}

func TestStore_IDCacheRoundTrip(t *testing.T) {
	s := testStore(t)
	if _, ok := s.Get("k"); ok {
		t.Fatal("expected miss on empty cache")
	}
	s.Set("k", "sp1")
	if got, ok := s.Get("k"); !ok || got != "sp1" {
		t.Errorf("Get = %q,%v want sp1,true", got, ok)
	}
	s.Set("k", "sp2") // overwrite
	if got, _ := s.Get("k"); got != "sp2" {
		t.Errorf("Get after overwrite = %q, want sp2", got)
	}
}

func TestStore_UpsertIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	id1, err := s.UpsertTrack(ctx, Track{SpotifyID: "sp1", Title: "Song", Artist: "Artist", Source: "test"})
	if err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}
	id2, err := s.UpsertTrack(ctx, Track{SpotifyID: "sp1", Title: "Song (Remastered)", Artist: "Artist"})
	if err != nil {
		t.Fatalf("UpsertTrack 2: %v", err)
	}
	if id1 != id2 {
		t.Errorf("track id changed on re-upsert: %s -> %s", id1, id2)
	}

	var n int
	if err := s.pool.QueryRow(ctx, "select count(*) from tracks").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("tracks count = %d, want 1 (idempotent)", n)
	}
	var title string
	if err := s.pool.QueryRow(ctx, "select title from tracks where spotify_id='sp1'").Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Song (Remastered)" {
		t.Errorf("title = %q, want updated value", title)
	}
}

func TestStore_PersistsCamelot(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if _, err := s.UpsertTrack(ctx, Track{SpotifyID: "sp1", Title: "Song", Artist: "Artist"}); err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}
	// key=9 (A), mode=0 (minor) -> Camelot 8A.
	if err := s.UpsertMixFeatures(ctx, "sp1", MixFeatures{Tempo: 120, Key: 9, Mode: 0, Loudness: -5}); err != nil {
		t.Fatalf("UpsertMixFeatures: %v", err)
	}
	var code string
	err := s.pool.QueryRow(ctx,
		"select camelot from mix_features mf join tracks t on t.id = mf.track_id where t.spotify_id = 'sp1'").Scan(&code)
	if err != nil {
		t.Fatal(err)
	}
	if code != "8A" {
		t.Errorf("camelot = %q, want 8A", code)
	}
}

func TestStore_UpsertMixFeaturesRejectsUnknownKey(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.UpsertTrack(ctx, Track{SpotifyID: "sp1", Title: "S", Artist: "A"}); err != nil {
		t.Fatal(err)
	}
	err := s.UpsertMixFeatures(ctx, "sp1", MixFeatures{Tempo: 120, Key: -1, Mode: 0, Loudness: -5})
	if !errors.Is(err, camelot.ErrUnknownKey) {
		t.Errorf("err = %v, want camelot.ErrUnknownKey", err)
	}
}

func TestStore_UpsertMixFeaturesUnknownTrack(t *testing.T) {
	s := testStore(t)
	err := s.UpsertMixFeatures(context.Background(), "missing", MixFeatures{Tempo: 120, Key: 0, Mode: 1, Loudness: -5})
	if err == nil {
		t.Error("expected an error when the track does not exist")
	}
}
