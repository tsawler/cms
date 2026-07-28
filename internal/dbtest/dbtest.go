// Package dbtest runs the CMS's store tests against real database engines in
// throwaway containers.
//
// The suite exists because the CMS speaks SQL to more than one engine: a test
// that passes on Postgres proves nothing about MySQL. Each registers a
// subtest per engine, so a single test body becomes the conformance check for
// every engine the CMS claims to support.
//
// Tests written against this harness should drive stores through their public
// API rather than raw SQL. A test that reaches for SQL directly has to be
// written once per dialect, which is exactly the duplication the harness is
// meant to remove.
//
// Containers are started lazily, once per engine per test process, and shared
// by every test in the package. Between tests, Truncate empties the cms_
// tables — far cheaper than a fresh database per test. When Docker is not
// available the whole suite skips rather than fails, so `go test ./...` still
// passes on a machine without it.
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql" // database/sql driver "mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
	"github.com/testcontainers/testcontainers-go"
	tcmariadb "github.com/testcontainers/testcontainers-go/modules/mariadb"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tsawler/cms/internal/dialect"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/migrations"
)

// Engine names one database engine under test.
type Engine string

const (
	Postgres Engine = "postgres"
	MySQL    Engine = "mysql"
	MariaDB  Engine = "mariadb"
)

// engines lists what Each runs against — every engine the CMS claims to
// support, so one test body is the conformance check for all of them.
//
// The MySQL floor is 8.0.31, the release that added EXCEPT, which the
// change-detection query in content/block.go depends on.
var engines = []Engine{Postgres, MySQL, MariaDB}

const (
	dbName = "cms_test"
	dbUser = "cms"
	dbPass = "cms"
)

// pool is one engine's lazily-started container and connection pool.
type pool struct {
	once sync.Once
	db   *sqldb.DB
	err  error
}

var pools = map[Engine]*pool{
	Postgres: {},
	MySQL:    {},
	MariaDB:  {},
}

// Each runs fn as a subtest against every engine under test, handing it a
// migrated, empty database. Use it as the entry point for any test that
// touches storage:
//
//	func TestPageInsert(t *testing.T) {
//	    dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
//	        store := content.NewStore(db, "en")
//	        ...
//	    })
//	}
func Each(t *testing.T, fn func(t *testing.T, db *sqldb.DB)) {
	t.Helper()
	if testing.Short() {
		t.Skip("dbtest: skipping database test in -short mode")
	}
	skipWithoutDocker(t)

	for _, engine := range engines {
		t.Run(string(engine), func(t *testing.T) {
			db := connect(t, engine)
			Truncate(t, db)
			fn(t, db)
		})
	}
}

// connect returns the shared pool for engine, starting its container on first
// use. A container failure fails every test that needs it, rather than being
// reported once and leaving the rest to fail obscurely.
func connect(t *testing.T, engine Engine) *sqldb.DB {
	t.Helper()
	p := pools[engine]
	p.once.Do(func() { p.db, p.err = start(engine) })
	if p.err != nil {
		t.Fatalf("dbtest: starting %s: %v", engine, p.err)
	}
	return p.db
}

// start boots a container for engine, waits for it to accept connections, and
// applies the CMS migrations. The container is reaped by testcontainers' Ryuk
// sidecar when the test process exits, so there is no teardown to forget.
func start(engine Engine) (*sqldb.DB, error) {
	ctx := context.Background()

	switch engine {
	case Postgres:
		container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase(dbName),
			tcpostgres.WithUsername(dbUser),
			tcpostgres.WithPassword(dbPass),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			return nil, fmt.Errorf("starting container: %w", err)
		}
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			return nil, fmt.Errorf("connection string: %w", err)
		}
		return open(ctx, "pgx", dsn, dialect.Postgres{})

	case MySQL:
		container, err := tcmysql.Run(ctx, "mysql:8.4",
			tcmysql.WithDatabase(dbName),
			tcmysql.WithUsername(dbUser),
			tcmysql.WithPassword(dbPass),
		)
		if err != nil {
			return nil, fmt.Errorf("starting container: %w", err)
		}
		dsn, err := container.ConnectionString(ctx, mysqlParams...)
		if err != nil {
			return nil, fmt.Errorf("connection string: %w", err)
		}
		return open(ctx, "mysql", dsn, dialect.MySQL{})

	case MariaDB:
		container, err := tcmariadb.Run(ctx, "mariadb:11.4",
			tcmariadb.WithDatabase(dbName),
			tcmariadb.WithUsername(dbUser),
			tcmariadb.WithPassword(dbPass),
		)
		if err != nil {
			return nil, fmt.Errorf("starting container: %w", err)
		}
		dsn, err := container.ConnectionString(ctx, mysqlParams...)
		if err != nil {
			return nil, fmt.Errorf("connection string: %w", err)
		}
		return open(ctx, "mysql", dsn, dialect.MySQL{})
	}
	return nil, fmt.Errorf("unknown engine %q", engine)
}

// mysqlParams are the DSN settings the CMS requires of a MySQL or MariaDB
// pool. They are documented on cms.Config.Dialect, and the example app sets
// the same ones.
//
//   - parseTime/loc: timestamps scan into time.Time, in UTC.
//   - time_zone: pins the server session to UTC too, so now() and a
//     Go-written time agree.
//   - clientFoundRows: makes an UPDATE report rows *matched* rather than
//     rows *changed*. Without it, re-saving a row with unchanged values
//     reports zero affected rows and the CMS reads that as "no such row".
var mysqlParams = []string{
	"parseTime=true",
	"loc=UTC",
	"time_zone=%27%2B00%3A00%27",
	"clientFoundRows=true",
}

// open connects, applies the schema, and wraps the pool in its dialect.
func open(ctx context.Context, driver, dsn string, d dialect.Dialect) (*sqldb.DB, error) {
	pool, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening pool: %w", err)
	}
	db := sqldb.New(pool, d)
	if err := migrate(ctx, db); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

// migrate applies the CMS schema, discarding the migration runner's output so
// a passing test run stays quiet.
func migrate(ctx context.Context, db *sqldb.DB) error {
	if err := migrations.Run(ctx, db, quietLogger()); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	return nil
}

// Truncate empties every cms_ table, leaving the schema in place. The table
// list is read from the database rather than hardcoded, so a new migration
// needs no change here.
func Truncate(t *testing.T, db *sqldb.DB) {
	t.Helper()
	ctx := context.Background()

	rows, err := db.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = $1 AND table_name LIKE 'cms\_%'`, schemaName(db))
	if err != nil {
		t.Fatalf("dbtest: listing tables: %v", err)
	}
	tables, err := sqldb.CollectRows(rows, func(row sqldb.Scanner) (string, error) {
		var name string
		err := row.Scan(&name)
		return name, err
	})
	if err != nil {
		t.Fatalf("dbtest: listing tables: %v", err)
	}

	var targets []string
	for _, name := range tables {
		// The migration ledger is schema, not data: truncating it would make
		// every later test re-run the migrations.
		if name != "cms_schema_migrations" {
			targets = append(targets, name)
		}
	}
	if len(targets) == 0 {
		return
	}

	if db.Dialect().Name() == "postgres" {
		// One statement, and CASCADE handles the foreign keys between them.
		stmt := "TRUNCATE TABLE " + strings.Join(targets, ", ") + " RESTART IDENTITY CASCADE"
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("dbtest: truncating: %v", err)
		}
		return
	}

	// MySQL has no CASCADE and refuses to truncate a table another row still
	// references, so foreign key checks come off for the duration. TRUNCATE
	// also resets AUTO_INCREMENT, matching RESTART IDENTITY.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("dbtest: reserving connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		t.Fatalf("dbtest: disabling foreign key checks: %v", err)
	}
	for _, name := range targets {
		if _, err := conn.Exec(ctx, "TRUNCATE TABLE `"+name+"`"); err != nil {
			t.Fatalf("dbtest: truncating %s: %v", name, err)
		}
	}
	if _, err := conn.Exec(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		t.Fatalf("dbtest: re-enabling foreign key checks: %v", err)
	}
}

// schemaName is what information_schema calls the database under test:
// Postgres organizes by schema, MySQL by database name.
func schemaName(db *sqldb.DB) string {
	if db.Dialect().Name() == "postgres" {
		return "public"
	}
	return dbName
}

// dockerOnce caches the Docker probe so a suite of skipped tests does not pay
// for a connection attempt each.
var (
	dockerOnce sync.Once
	dockerErr  error
)

// skipWithoutDocker skips the calling test when there is no usable Docker
// daemon, so the unit-test suite still runs on machines without one.
func skipWithoutDocker(t *testing.T) {
	t.Helper()
	dockerOnce.Do(func() {
		provider, err := testcontainers.NewDockerProvider()
		if err != nil {
			dockerErr = err
			return
		}
		defer provider.Close()
		dockerErr = provider.Health(context.Background())
	})
	if dockerErr != nil {
		t.Skipf("dbtest: skipping, no usable Docker daemon (%v)", dockerErr)
	}
}

// quietLogger returns a logger that discards everything, keeping migration
// chatter out of test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
