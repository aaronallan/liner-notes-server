package ingest

import (
	"context"
	"os"
	"testing"

	"github.com/aaronpollock/liner-notes-server/internal/lookup"
	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
	"github.com/aaronpollock/liner-notes-server/internal/store"
)

// testStore opens a migrated, empty store, or skips when TEST_DATABASE_URL is unset.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration tests")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := s.Pool().Exec(ctx, "truncate id_cache, mix_features, tracks restart identity cascade"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestIngest_Integration_StoresAndIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	looker := fakeLooker{fn: func(req lookup.Request) (lookup.Result, error) {
		return availableResult("sp-"+req.Title, &reccobeats.AudioFeatures{Tempo: 120, Key: 9, Mode: 0, Loudness: -5}), nil
	}}
	ing := New(looker, s)

	items := []Item{{Title: "a", Artist: "A"}, {Title: "b", Artist: "B"}}
	for range 2 { // ingest twice — must be idempotent
		summary := ing.IngestList(ctx, items)
		if summary.Ingested != 2 {
			t.Fatalf("summary = %+v, want 2 ingested", summary)
		}
	}

	var tracks, feats int
	if err := s.Pool().QueryRow(ctx, "select count(*) from tracks").Scan(&tracks); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool().QueryRow(ctx, "select count(*) from mix_features").Scan(&feats); err != nil {
		t.Fatal(err)
	}
	if tracks != 2 || feats != 2 {
		t.Errorf("tracks=%d features=%d, want 2/2 (idempotent)", tracks, feats)
	}
}
