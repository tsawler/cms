// Package sqldb wraps database/sql with the CMS's dialect translation.
//
// Its method set deliberately mirrors pgx's — Query/QueryRow/Exec take a
// context first, and Tx.Commit/Rollback take one too — so store code reads
// the same regardless of which engine is underneath and did not have to be
// restructured when the CMS stopped being Postgres-only. Every statement
// passes through the dialect on its way to the driver, so stores can keep
// writing canonical Postgres SQL.
package sqldb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/tsawler/cms/internal/dialect"
)

// Scanner is the one-row interface the scan helpers accept. Both *sql.Row
// and *sql.Rows satisfy it, so a helper written for a single-row lookup also
// works inside CollectRows.
type Scanner interface {
	Scan(dest ...any) error
}

// Result reports how many rows a statement changed.
//
// RowsAffected drops the error database/sql returns with it, matching the
// pgx command tag the CMS was written against. Both supported drivers always
// report it, and the count is only ever used to tell "no such row" from a
// successful write.
type Result struct{ res sql.Result }

// RowsAffected returns the number of rows the statement changed.
func (r Result) RowsAffected() int64 {
	if r.res == nil {
		return 0
	}
	n, _ := r.res.RowsAffected()
	return n
}

// raw is the part of database/sql that DB and Tx share.
type raw interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB is a connection pool that speaks the CMS's canonical SQL.
type DB struct {
	db *sql.DB
	d  dialect.Dialect
}

// New wraps an open *sql.DB with the given dialect.
func New(db *sql.DB, d dialect.Dialect) *DB {
	return &DB{db: db, d: d}
}

// Dialect returns the dialect in use, for the few places that must render a
// fragment differently per engine.
func (db *DB) Dialect() dialect.Dialect { return db.d }

// SQL returns the underlying pool, for callers that need database/sql
// directly (the migration runner takes a dedicated connection from it).
func (db *DB) SQL() *sql.DB { return db.db }

func (db *DB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	return exec(ctx, db.db, db.d, query, args)
}

func (db *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return query_(ctx, db.db, db.d, query, args)
}

func (db *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return queryRow(ctx, db.db, db.d, query, args)
}

// InsertID runs an INSERT written *without* a RETURNING clause and reports
// the generated id. The dialect decides how: Postgres appends RETURNING id,
// MySQL reads LastInsertId.
func (db *DB) InsertID(ctx context.Context, query string, args ...any) (int64, error) {
	return db.d.InsertID(ctx, execer{db.db, db.d}, query, args...)
}

// Conn reserves a single connection from the pool. Session-scoped state —
// an advisory lock, a SET — only means anything when taken and released on
// one connection, which a pool otherwise gives no guarantee of.
func (db *DB) Conn(ctx context.Context) (*Conn, error) {
	c, err := db.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &Conn{conn: c, d: db.d}, nil
}

// Conn is a single reserved connection.
type Conn struct {
	conn *sql.Conn
	d    dialect.Dialect
}

// Execer exposes the connection to the dialect helpers.
func (c *Conn) Execer() dialect.Execer { return execer{c.conn, c.d} }

// Exec runs a statement on this connection.
func (c *Conn) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	return exec(ctx, c.conn, c.d, query, args)
}

// Close returns the connection to the pool.
func (c *Conn) Close() error { return c.conn.Close() }

// Begin starts a transaction.
func (db *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx, d: db.d}, nil
}

// Tx is an open transaction. Commit and Rollback take a context they do not
// use, so that store code reads identically to the pooled path.
type Tx struct {
	tx *sql.Tx
	d  dialect.Dialect
}

func (tx *Tx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	return exec(ctx, tx.tx, tx.d, query, args)
}

func (tx *Tx) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return query_(ctx, tx.tx, tx.d, query, args)
}

func (tx *Tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return queryRow(ctx, tx.tx, tx.d, query, args)
}

// InsertID runs an INSERT without a RETURNING clause inside the transaction
// and reports the generated id.
func (tx *Tx) InsertID(ctx context.Context, query string, args ...any) (int64, error) {
	return tx.d.InsertID(ctx, execer{tx.tx, tx.d}, query, args...)
}

// Dialect returns the dialect in use, matching DB.Dialect so a statement
// built inside a transaction reads the same as one built outside it.
func (tx *Tx) Dialect() dialect.Dialect { return tx.d }

func (tx *Tx) Commit(context.Context) error   { return tx.tx.Commit() }
func (tx *Tx) Rollback(context.Context) error { return tx.tx.Rollback() }

func exec(ctx context.Context, r raw, d dialect.Dialect, query string, args []any) (Result, error) {
	q, a := d.Rewrite(query, args)
	res, err := r.ExecContext(ctx, q, a...)
	return Result{res}, err
}

func query_(ctx context.Context, r raw, d dialect.Dialect, query string, args []any) (*sql.Rows, error) {
	q, a := d.Rewrite(query, args)
	return r.QueryContext(ctx, q, a...)
}

func queryRow(ctx context.Context, r raw, d dialect.Dialect, query string, args []any) *sql.Row {
	q, a := d.Rewrite(query, args)
	return r.QueryRowContext(ctx, q, a...)
}

// execer adapts a raw handle to the interface the dialect helpers take.
type execer struct {
	raw raw
	d   dialect.Dialect
}

func (e execer) ExecContext(ctx context.Context, query string, args ...any) (dialect.Result, error) {
	q, a := e.d.Rewrite(query, args)
	return e.raw.ExecContext(ctx, q, a...)
}

func (e execer) QueryRowContext(ctx context.Context, query string, args ...any) dialect.Row {
	q, a := e.d.Rewrite(query, args)
	return e.raw.QueryRowContext(ctx, q, a...)
}

// CollectRows reads every row through fn and returns the results, closing
// rows and reporting any iteration error. It replaces pgx.CollectRows.
func CollectRows[T any](rows *sql.Rows, fn func(Scanner) (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := fn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// JSON binds m to a JSON column. A nil map is stored as NULL, which the
// snippet store uses to mean "a plain block rather than a section preset".
//
// pgx did this conversion itself; database/sql does not, so the JSON columns
// are wrapped explicitly at each bind and scan site.
func JSON(m map[string]string) driver.Valuer { return jsonMap(m) }

type jsonMap map[string]string

func (m jsonMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(map[string]string(m))
	if err != nil {
		return nil, err
	}
	// Returned as a string so both drivers send it as a text value; MySQL's
	// JSON columns and Postgres's jsonb both accept that.
	return string(b), nil
}

// JSONInto decodes a JSON column into *m. A NULL column leaves m nil.
func JSONInto(m *map[string]string) sql.Scanner { return jsonScanner{m} }

type jsonScanner struct{ dst *map[string]string }

func (s jsonScanner) Scan(src any) error {
	var data []byte
	switch v := src.(type) {
	case nil:
		*s.dst = nil
		return nil
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("sqldb: cannot scan %T into a JSON map", src)
	}
	if len(data) == 0 {
		*s.dst = nil
		return nil
	}
	return json.Unmarshal(data, s.dst)
}
