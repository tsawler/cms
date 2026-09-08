// Package sessionstore implements the scs.Store interface on top of the
// cms_sessions table. It exists (rather than using scs's own pgxstore) so
// the table name carries the cms_ prefix and can live in a database shared
// with the host application.
package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/tsawler/cms/internal/sessiondata"
	"github.com/tsawler/cms/internal/sqldb"
)

// scs reaches for the *Ctx methods by asserting on them one at a time, so
// a signature that drifts would be silently ignored rather than caught —
// the store would go on working, on context.Background(), with nothing to
// say it had stopped honouring cancellation. This is the assertion that
// notices.
var _ scs.CtxStore = (*Store)(nil)

// Store persists sessions in the cms_sessions table.
type Store struct {
	db          *sqldb.DB
	stopCleanup chan struct{}
	stopOnce    sync.Once
	// closed when the cleanup goroutine has returned, so a caller (or a
	// test) can tell "asked it to stop" from "it has stopped".
	cleanupDone chan struct{}
}

// New returns a Store that deletes expired sessions once an hour.
func New(db *sqldb.DB) *Store {
	return NewWithCleanupInterval(db, time.Hour)
}

// NewWithCleanupInterval returns a Store whose background cleanup runs at
// the given interval. A zero or negative interval disables cleanup.
func NewWithCleanupInterval(db *sqldb.DB, interval time.Duration) *Store {
	s := &Store{db: db}
	if interval > 0 {
		s.stopCleanup = make(chan struct{})
		s.cleanupDone = make(chan struct{})
		go s.cleanupLoop(interval)
	}
	return s
}

// The Store/CtxStore pair. scs prefers the *Ctx methods when a store has
// them and falls back to the plain ones otherwise, so the plain three
// stay for anyone holding this store through the narrower interface —
// they are the context-less spelling of the same three queries, and every
// session the CMS itself serves goes through the Ctx side.
//
// Before those existed, every session read and write ran on
// context.Background(): no deadline, and no notice when the client went
// away, so a slow database turned each abandoned request into a query
// nothing would stop.

// Find returns the data for a session token, if present and unexpired.
func (s *Store) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

// FindCtx is Find under the request's own context, so a lookup for a
// visitor who is no longer there stops with them.
func (s *Store) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	var data []byte
	err := s.db.QueryRow(ctx,
		"SELECT data FROM cms_sessions WHERE token = $1 AND expiry > now()", token,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Commit inserts or updates a session token with data and expiry.
func (s *Store) Commit(token string, data []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, data, expiry)
}

// CommitCtx is Commit under a write context: deadlined, but not
// cancellable by the request. See sessiondata.WriteContext.
func (s *Store) CommitCtx(ctx context.Context, token string, data []byte, expiry time.Time) error {
	ctx, cancel := sessiondata.WriteContext(ctx)
	defer cancel()
	_, err := s.db.Exec(ctx, `
		INSERT INTO cms_sessions (token, data, expiry) VALUES ($1, $2, $3)
		ON CONFLICT (token) DO UPDATE SET data = EXCLUDED.data, expiry = EXCLUDED.expiry`,
		token, data, expiry)
	return err
}

// Delete removes a session token.
func (s *Store) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}

// DeleteCtx is Delete under a write context. A logout that did not land
// because the browser hung up would leave a session that still works, so
// this one in particular outlives the request that asked for it.
func (s *Store) DeleteCtx(ctx context.Context, token string) error {
	ctx, cancel := sessiondata.WriteContext(ctx)
	defer cancel()
	_, err := s.db.Exec(ctx,
		"DELETE FROM cms_sessions WHERE token = $1", token)
	return err
}

// StopCleanup halts the background cleanup goroutine, for tests or
// graceful shutdown. It returns once the goroutine has actually stopped,
// and is safe to call any number of times — including on a store built
// with cleanup disabled.
func (s *Store) StopCleanup() {
	if s.stopCleanup == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCleanup) })
	<-s.cleanupDone
}

func (s *Store) cleanupLoop(interval time.Duration) {
	defer close(s.cleanupDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_, _ = s.db.Exec(context.Background(),
				"DELETE FROM cms_sessions WHERE expiry < now()")
		case <-s.stopCleanup:
			return
		}
	}
}
