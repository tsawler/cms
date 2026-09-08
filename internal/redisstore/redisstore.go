// Package redisstore implements the scs.Store interface on top of Redis.
// It exists (rather than using scs's own redisstore) so the keys carry the
// cms_session: prefix and can live in a Redis instance shared with the host
// application, mirroring what sessionstore does for the cms_sessions table.
//
// Redis expires keys itself, so unlike sessionstore there is no background
// cleanup goroutine.
package redisstore

import (
	"context"
	"errors"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tsawler/cms/internal/sessiondata"
)

// Same assertion as sessionstore's, for the same reason: scs picks the
// *Ctx methods by duck-typing, so a drifted signature falls back to
// context.Background() without a word.
var _ scs.CtxStore = (*Store)(nil)

const keyPrefix = "cms_session:"

// Store persists sessions in Redis under cms_session: keys.
type Store struct {
	client redis.UniversalClient
}

// New returns a Store backed by client.
func New(client redis.UniversalClient) *Store {
	return &Store{client: client}
}

// The Store/CtxStore pair, on the same terms as the SQL store: reads take
// the request's context, writes take a deadlined one that a disconnect
// cannot cancel. See sessiondata.WriteContext for why they differ.

// Find returns the data for a session token, if present and unexpired.
func (s *Store) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

// FindCtx is Find under the request's own context.
func (s *Store) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	data, err := s.client.Get(ctx, keyPrefix+token).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Commit inserts or updates a session token with data, expiring at expiry.
func (s *Store) Commit(token string, data []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, data, expiry)
}

// CommitCtx is Commit under a write context.
func (s *Store) CommitCtx(ctx context.Context, token string, data []byte, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return s.DeleteCtx(ctx, token)
	}
	wctx, cancel := sessiondata.WriteContext(ctx)
	defer cancel()
	return s.client.Set(wctx, keyPrefix+token, data, ttl).Err()
}

// Delete removes a session token. Deleting an absent token is not an error.
func (s *Store) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}

// DeleteCtx is Delete under a write context.
func (s *Store) DeleteCtx(ctx context.Context, token string) error {
	wctx, cancel := sessiondata.WriteContext(ctx)
	defer cancel()
	return s.client.Del(wctx, keyPrefix+token).Err()
}
