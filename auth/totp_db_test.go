package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// The store half of two-factor: enabling writes the secret and spends the
// confirming code's step, each step is claimable exactly once, and
// disabling clears everything.
func TestTOTPStoreLifecycle(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		u := seedUser(t, s, auth.User{Active: true})

		got, err := s.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.TwoFactorEnabled() {
			t.Fatal("fresh user reports two-factor enabled")
		}

		const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
		if err := s.EnableTOTP(ctx, u.ID, secret, 100); err != nil {
			t.Fatalf("EnableTOTP: %v", err)
		}
		got, err = s.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.TwoFactorEnabled() || got.TOTPSecret != secret {
			t.Fatalf("after enable: secret = %q", got.TOTPSecret)
		}
		if got.TOTPLastStep != 100 {
			t.Fatalf("after enable: last step = %d, want 100", got.TOTPLastStep)
		}

		// The confirming step is spent; a later step claims once, only once.
		if ok, _ := s.ConsumeTOTPStep(ctx, u.ID, 100); ok {
			t.Error("the enrollment-confirming step was claimable again")
		}
		if ok, _ := s.ConsumeTOTPStep(ctx, u.ID, 101); !ok {
			t.Error("a fresh step was not claimable")
		}
		if ok, _ := s.ConsumeTOTPStep(ctx, u.ID, 101); ok {
			t.Error("a spent step was claimable twice (replay)")
		}
		if ok, _ := s.ConsumeTOTPStep(ctx, u.ID, 99); ok {
			t.Error("an older step was claimable after a newer one")
		}

		if err := s.DisableTOTP(ctx, u.ID); err != nil {
			t.Fatalf("DisableTOTP: %v", err)
		}
		got, err = s.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.TwoFactorEnabled() || got.TOTPLastStep != 0 {
			t.Errorf("after disable: secret = %q, last step = %d", got.TOTPSecret, got.TOTPLastStep)
		}

		// Unknown users get ErrNotFound, matching the other user writes.
		if err := s.EnableTOTP(ctx, 99999, secret, 1); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("EnableTOTP(unknown) = %v, want ErrNotFound", err)
		}
		if err := s.DisableTOTP(ctx, 99999); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("DisableTOTP(unknown) = %v, want ErrNotFound", err)
		}
	})
}
