package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies any embedded migrations that have not yet run, in filename
// order, recording each in schema_migrations. It is safe to call on every
// startup: already-applied migrations are skipped.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx,
		`create table if not exists schema_migrations (
		   version    text primary key,
		   applied_at timestamptz not null default now()
		 )`); err != nil {
		return fmt.Errorf("store: ensure schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		applied, err := s.migrationApplied(ctx, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := s.applyMigration(ctx, name); err != nil {
			return fmt.Errorf("store: apply %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) migrationApplied(ctx context.Context, version string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		"select exists(select 1 from schema_migrations where version = $1)", version).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: check migration %s: %w", version, err)
	}
	return exists, nil
}

func (s *Store) applyMigration(ctx context.Context, name string) error {
	body, err := migrationFS.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	if _, err := tx.Exec(ctx, string(body)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "insert into schema_migrations (version) values ($1)", name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
