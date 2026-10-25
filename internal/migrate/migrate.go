// Package migrate applies ordered, versioned SQL migrations.
//
// It replaces the previous approach of executing a bag of
// CREATE TABLE IF NOT EXISTS statements on every boot, which had no version
// history, no way to detect drift, and raced when more than one replica started
// at once. Guarantees provided here:
//
//   - A Postgres advisory lock serialises migration across replicas, so
//     starting N pods concurrently applies each migration exactly once.
//   - Applied versions are recorded in <schema>.schema_migrations.
//   - Each migration runs inside its own transaction: a failure rolls back
//     cleanly rather than leaving a half-applied schema.
//   - Checksums of applied migrations are verified on every boot, so editing a
//     migration that already shipped is reported instead of silently ignored.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration is a single ordered schema change. Version must be unique and
// ascending within a service; SQL must be a complete, self-contained script.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Run applies every pending migration for schema, in version order.
//
// schema is both the Postgres schema that owns the migration ledger and the
// advisory-lock namespace, so each service migrates independently.
func Run(ctx context.Context, pool *pgxpool.Pool, schema string, migrations []Migration) error {
	if err := validate(migrations); err != nil {
		return err
	}

	ordered := make([]Migration, len(migrations))
	copy(ordered, migrations)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Version < ordered[j].Version })

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer conn.Release()

	// Serialise across replicas. pg_advisory_lock is held for the session and
	// released explicitly below, so a crashed pod's lock dies with its
	// connection rather than blocking the next deploy forever.
	lockID := advisoryLockID(schema)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return fmt.Errorf("migrate: acquire advisory lock: %w", err)
	}
	defer func() {
		// Best effort. If this fails the lock is still released when the session
		// ends, which is why the error is not propagated: the migrations have
		// already been applied and committed by this point.
		_, _ = conn.Exec(context.WithoutCancel(ctx), //nolint:errcheck // released on session end regardless
			`SELECT pg_advisory_unlock($1)`, lockID)
	}()

	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, quoteIdent(schema))); err != nil {
		return fmt.Errorf("migrate: create schema %s: %w", schema, err)
	}

	if _, err := conn.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.schema_migrations (
			version    INT         NOT NULL PRIMARY KEY,
			name       TEXT        NOT NULL,
			checksum   TEXT        NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, quoteIdent(schema))); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}

	applied, err := loadApplied(ctx, conn.Conn(), schema)
	if err != nil {
		return err
	}

	for _, m := range ordered {
		sum := checksum(m.SQL)

		if prev, ok := applied[m.Version]; ok {
			if prev != sum {
				return fmt.Errorf(
					"migrate: %s migration %d (%s) was modified after being applied "+
						"(recorded checksum %s, current %s) — add a new migration instead of editing this one",
					schema, m.Version, m.Name, prev, sum)
			}
			continue
		}

		if err := applyOne(ctx, conn.Conn(), schema, m, sum); err != nil {
			return err
		}
	}

	return nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, schema string, m Migration, sum string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %s begin migration %d: %w", schema, m.Version, err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // rollback after commit is a no-op

	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("migrate: %s migration %d (%s): %w", schema, m.Version, m.Name, err)
	}

	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s.schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		quoteIdent(schema)), m.Version, m.Name, sum); err != nil {
		return fmt.Errorf("migrate: %s record migration %d: %w", schema, m.Version, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: %s commit migration %d: %w", schema, m.Version, err)
	}
	return nil
}

func loadApplied(ctx context.Context, conn *pgx.Conn, schema string) (map[int]string, error) {
	rows, err := conn.Query(ctx, fmt.Sprintf(
		`SELECT version, checksum FROM %s.schema_migrations`, quoteIdent(schema)))
	if err != nil {
		return nil, fmt.Errorf("migrate: read applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]string)
	for rows.Next() {
		var version int
		var sum string
		if err := rows.Scan(&version, &sum); err != nil {
			return nil, fmt.Errorf("migrate: scan applied migration: %w", err)
		}
		applied[version] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: iterate applied migrations: %w", err)
	}
	return applied, nil
}

func validate(migrations []Migration) error {
	seen := make(map[int]string, len(migrations))
	for _, m := range migrations {
		if m.Version <= 0 {
			return fmt.Errorf("migrate: migration %q has non-positive version %d", m.Name, m.Version)
		}
		if m.SQL == "" {
			return fmt.Errorf("migrate: migration %d (%s) has empty SQL", m.Version, m.Name)
		}
		if other, dup := seen[m.Version]; dup {
			return fmt.Errorf("migrate: duplicate version %d used by both %q and %q", m.Version, other, m.Name)
		}
		seen[m.Version] = m.Name
	}
	return nil
}

func checksum(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

// advisoryLockID derives a stable 63-bit lock id from the schema name.
func advisoryLockID(schema string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("ledger.migrate:" + schema))
	// Mask the sign bit: pg_advisory_lock takes a signed bigint and a stable
	// non-negative value keeps the id readable in pg_locks.
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)
}

// quoteIdent renders a SQL identifier safely. Schema names here come from
// service constants, never user input, but quoting keeps that guarantee local.
func quoteIdent(ident string) string {
	return pgx.Identifier{ident}.Sanitize()
}
