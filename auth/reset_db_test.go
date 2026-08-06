package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

func TestResetMintAndConsume(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		u := seedUser(t, s, auth.User{Email: "reset@example.com", Active: true})

		token, err := s.MintReset(ctx, u.ID)
		if err != nil {
			t.Fatalf("minting: %v", err)
		}
		if token == "" {
			t.Fatal("minted an empty token")
		}

		// Peeking does not spend it.
		for i := 0; i < 2; i++ {
			got, err := s.ResetUser(ctx, token)
			if err != nil {
				t.Fatalf("peek %d: %v", i, err)
			}
			if got.ID != u.ID {
				t.Fatalf("peek returned user %d, want %d", got.ID, u.ID)
			}
		}

		got, err := s.ConsumeReset(ctx, token)
		if err != nil {
			t.Fatalf("consuming: %v", err)
		}
		if got.ID != u.ID {
			t.Fatalf("consume returned user %d, want %d", got.ID, u.ID)
		}

		// Single use: the same token again is dead, by the same error an
		// unknown token gets.
		if _, err := s.ConsumeReset(ctx, token); !errors.Is(err, auth.ErrResetInvalid) {
			t.Fatalf("second consume: got %v, want ErrResetInvalid", err)
		}
		if _, err := s.ResetUser(ctx, token); !errors.Is(err, auth.ErrResetInvalid) {
			t.Fatalf("peek after consume: got %v, want ErrResetInvalid", err)
		}
	})
}

func TestResetUnknownToken(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := auth.NewStore(db)
		if _, err := s.ResetUser(context.Background(), "no-such-token"); !errors.Is(err, auth.ErrResetInvalid) {
			t.Fatalf("got %v, want ErrResetInvalid", err)
		}
	})
}

// TestResetSecondMintRevokesFirst pins the one-live-link rule: asking
// twice must leave only the newest link working, or a forgotten first
// email is a second credential floating around.
func TestResetSecondMintRevokesFirst(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		u := seedUser(t, s, auth.User{Email: "twice@example.com", Active: true})

		first, err := s.MintReset(ctx, u.ID)
		if err != nil {
			t.Fatalf("first mint: %v", err)
		}
		second, err := s.MintReset(ctx, u.ID)
		if err != nil {
			t.Fatalf("second mint: %v", err)
		}

		if _, err := s.ResetUser(ctx, first); !errors.Is(err, auth.ErrResetInvalid) {
			t.Fatalf("first token still live after second mint: %v", err)
		}
		if _, err := s.ConsumeReset(ctx, second); err != nil {
			t.Fatalf("second token should work: %v", err)
		}
	})
}

// TestResetTokensAreDistinct: two users' tokens must not collide, and one
// user's token must not open another's account.
func TestResetTokensAreDistinct(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		a := seedUser(t, s, auth.User{Email: "a@example.com", Active: true})
		b := seedUser(t, s, auth.User{Email: "b@example.com", Active: true})

		ta, err := s.MintReset(ctx, a.ID)
		if err != nil {
			t.Fatalf("minting for a: %v", err)
		}
		tb, err := s.MintReset(ctx, b.ID)
		if err != nil {
			t.Fatalf("minting for b: %v", err)
		}
		if ta == tb {
			t.Fatal("two users were minted the same token")
		}

		got, err := s.ConsumeReset(ctx, ta)
		if err != nil {
			t.Fatalf("consuming a's token: %v", err)
		}
		if got.ID != a.ID {
			t.Fatalf("a's token resolved to user %d", got.ID)
		}
	})
}
