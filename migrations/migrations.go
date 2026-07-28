// Package migrations creates and upgrades the CMS's database schema from
// SQL files embedded in the module, so host applications need no external
// migration tool.
//
// Each supported engine has its own directory of migration files under sql/,
// sharing one version sequence: sql/postgres/0007_x.sql and
// sql/mysql/0007_x.sql are the same change expressed twice. The DDL dialects
// differ too much to translate mechanically — identity columns, timestamp
// types, and the fact that MySQL cannot index a TEXT column all need a human
// decision — so they are maintained side by side.
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

	"github.com/tsawler/cms/internal/sqldb"
)

//go:embed sql/postgres/*.sql sql/mysql/*.sql
var sqlFS embed.FS

// lockName serializes concurrent migration runs, e.g. two instances of the
// host app starting at once.
const lockName = "cms_schema_migrations"

// Run applies any migrations that have not yet been applied, in version
// order. It is safe to call on every startup.
//
// On Postgres each file runs in its own transaction, so a failure rolls
// back. MySQL and MariaDB commit DDL implicitly, so a failure there can
// leave a partially applied migration needing manual repair; the version is
// only recorded on success, so a re-run retries the whole file.
func Run(ctx context.Context, db *sqldb.DB, logger *slog.Logger) error {
	d := db.Dialect()

	// A dedicated connection, because the advisory lock is session-scoped:
	// it must be taken and released on one connection to serialize anything.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("cms migrations: acquiring connection: %w", err)
	}
	defer conn.Close()

	unlock, err := d.Lock(ctx, conn.Execer(), lockName)
	if err != nil {
		return fmt.Errorf("cms migrations: %w", err)
	}
	defer unlock()

	if err := ensureLedger(ctx, db); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	dir := "sql/" + d.MigrationDir()
	entries, err := fs.ReadDir(sqlFS, dir)
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
		script, err := sqlFS.ReadFile(dir + "/" + name)
		if err != nil {
			return err
		}
		if err := apply(ctx, db, version, name, string(script)); err != nil {
			return err
		}
		logger.Info("cms: applied migration", "file", name, "engine", d.Name())
	}
	return nil
}

// ensureLedger creates the table recording which migrations have run. It is
// written in the portable subset both engines accept, since it predates any
// dialect-specific schema.
func ensureLedger(ctx context.Context, db *sqldb.DB) error {
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS cms_schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL
		)`); err != nil {
		return fmt.Errorf("cms migrations: creating migrations table: %w", err)
	}
	return nil
}

// appliedVersions reads the set of versions already applied.
func appliedVersions(ctx context.Context, db *sqldb.DB) (map[int]bool, error) {
	rows, err := db.Query(ctx, "SELECT version FROM cms_schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("cms migrations: reading applied versions: %w", err)
	}
	versions, err := sqldb.CollectRows(rows, func(row sqldb.Scanner) (int, error) {
		var v int
		err := row.Scan(&v)
		return v, err
	})
	if err != nil {
		return nil, fmt.Errorf("cms migrations: reading applied versions: %w", err)
	}
	applied := make(map[int]bool, len(versions))
	for _, v := range versions {
		applied[v] = true
	}
	return applied, nil
}

// apply runs one migration file and records its version, in a transaction.
// Postgres honours that fully; MySQL commits its DDL as it goes and can only
// roll back the ledger insert.
func apply(ctx context.Context, db *sqldb.DB, version int, name, script string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cms migrations: beginning tx for %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Postgres takes the whole file in one Exec; MySQL's driver rejects
	// multiple statements per call, so the dialect splits it.
	for _, stmt := range db.Dialect().SplitStatements(script) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("cms migrations: applying %s: %w", name, err)
		}
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO cms_schema_migrations (version, applied_at) VALUES ($1, now())", version); err != nil {
		return fmt.Errorf("cms migrations: recording %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("cms migrations: committing %s: %w", name, err)
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
