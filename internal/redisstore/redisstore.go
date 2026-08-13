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

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "cms_session:"

// Store persists sessions in Redis under cms_session: keys.
type Store struct {
	client redis.UniversalClient
}

// New returns a Store backed by client.
func New(client redis.UniversalClient) *Store {
	return &Store{client: client}
}

// Find returns the data for a session token, if present and unexpired.
func (s *Store) Find(token string) ([]byte, bool, error) {
	data, err := s.client.Get(context.Background(), keyPrefix+token).Bytes()
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
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return s.Delete(token)
	}
	return s.client.Set(context.Background(), keyPrefix+token, data, ttl).Err()
}

// Delete removes a session token. Deleting an absent token is not an error.
func (s *Store) Delete(token string) error {
	return s.client.Del(context.Background(), keyPrefix+token).Err()
}
