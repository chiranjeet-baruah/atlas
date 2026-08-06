package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrateAndConnect runs every migration in dir using golang-migrate, then
// returns a pool with the pgvector codec registered. Migrations must run
// first: the codec registration in Connect depends on the `vector`
// extension already existing, which fails with "vector type not found in
// the database" if attempted any earlier.
func MigrateAndConnect(ctx context.Context, connString, dir string) (*pgxpool.Pool, error) {
	if err := RunMigrations(connString, dir); err != nil {
		return nil, err
	}
	return Connect(ctx, connString)
}

// RunMigrations applies every migration in dir, in version order, via
// golang-migrate's pgx v5 driver.
//
// `app` and `worker` both call this on startup, so concurrent runs against
// the same fresh database are expected — e.g. `CREATE EXTENSION IF NOT
// EXISTS` is not safe under concurrent execution, since two sessions can
// both see "extension does not exist yet" and race on Postgres's
// pg_extension unique index. golang-migrate's Postgres driver takes its
// own pg_advisory_lock for the duration of a run, so this races safely
// without any locking code of our own — see
// TestMigrateAndConnect_ConcurrentCallersDoNotRace.
func RunMigrations(connString, dir string) error {
	m, err := migrate.New("file://"+dir, toMigrateURL(connString))
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// toMigrateURL rewrites a postgres:// or postgresql:// connection URL to
// the pgx5:// scheme golang-migrate's pgx v5 driver requires — the rest of
// the codebase uses postgres/postgresql URLs (pgxpool, testcontainers), so
// this keeps one connection string as the single source of truth.
func toMigrateURL(connString string) string {
	if idx := strings.Index(connString, "://"); idx != -1 {
		return "pgx5" + connString[idx:]
	}
	return connString
}
