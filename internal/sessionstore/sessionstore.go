// Package sessionstore implements the scs.Store interface on top of the
// cms_sessions table. It exists (rather than using scs's own pgxstore) so
// the table name carries the cms_ prefix and can live in a database shared
// with the host application.
package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/tsawler/cms/internal/sqldb"
)

// Store persists sessions in the cms_sessions table.
type Store struct {
	db          *sqldb.DB
	stopCleanup chan struct{}
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
		go s.cleanupLoop(interval)
	}
	return s
}

// Find returns the data for a session token, if present and unexpired.
func (s *Store) Find(token string) ([]byte, bool, error) {
	var data []byte
	err := s.db.QueryRow(context.Background(),
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
	_, err := s.db.Exec(context.Background(), `
		INSERT INTO cms_sessions (token, data, expiry) VALUES ($1, $2, $3)
		ON CONFLICT (token) DO UPDATE SET data = EXCLUDED.data, expiry = EXCLUDED.expiry`,
		token, data, expiry)
	return err
}

// Delete removes a session token.
func (s *Store) Delete(token string) error {
	_, err := s.db.Exec(context.Background(),
		"DELETE FROM cms_sessions WHERE token = $1", token)
	return err
}

// StopCleanup halts the background cleanup goroutine, for tests or graceful
// shutdown. It is safe to call once.
func (s *Store) StopCleanup() {
	if s.stopCleanup != nil {
		close(s.stopCleanup)
	}
}

func (s *Store) cleanupLoop(interval time.Duration) {
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
