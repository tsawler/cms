// Package migrations creates and upgrades the CMS's database schema from
// SQL files embedded in the module, so host applications need no external
// migration tool.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var sqlFS embed.FS

// advisoryLockKey serializes concurrent migration runs (e.g. two instances
// of the host app starting at once). Arbitrary but stable.
const advisoryLockKey int64 = 746_111_551_253_09

// Run applies any migrations that have not yet been applied, in version
// order, each in its own transaction. It is safe to call on every startup.
func Run(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) error {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("cms migrations: acquiring connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("cms migrations: acquiring advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS cms_schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("cms migrations: creating migrations table: %w", err)
	}

	applied := map[int]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM cms_schema_migrations")
	if err != nil {
		return fmt.Errorf("cms migrations: reading applied versions: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := fs.ReadDir(sqlFS, "sql")
	if err != nil {
		return fmt.Errorf("cms migrations: reading embedded sql: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		name := entry.Name()
		version, err := versionOf(name)
		if err != nil {
			return err
		}
		if applied[version] {
			continue
		}
		sqlBytes, err := sqlFS.ReadFile("sql/" + name)
		if err != nil {
			return err
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("cms migrations: beginning tx for %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("cms migrations: applying %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO cms_schema_migrations (version) VALUES ($1)", version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("cms migrations: recording %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("cms migrations: committing %s: %w", name, err)
		}
		logger.Info("cms: applied migration", "file", name)
	}
	return nil
}

// versionOf extracts the numeric prefix of a migration filename, e.g.
// "0001_users.sql" -> 1.
func versionOf(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("cms migrations: %s: name must look like 0001_description.sql", name)
	}
	v, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("cms migrations: %s: invalid version prefix: %w", name, err)
	}
	return v, nil
}
