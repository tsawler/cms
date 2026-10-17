package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// A user-form save lands whole or not at all. A grant too long for its
// column is what fails here, after the account row has been written.
func TestSaveIsAllOrNothing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		bad := []auth.Permission{auth.Permission(strings.Repeat("p", 600))}

		created := &auth.User{Email: "new@example.com", Name: "New", PasswordHash: "x", Role: auth.RoleEditor, Active: true}
		if err := s.Save(ctx, auth.UserSave{User: created, Permissions: bad}); err == nil {
			t.Fatal("Save accepted a grant longer than its column")
		}
		if _, err := s.GetByEmail(ctx, "new@example.com"); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("a failed create left the account behind (GetByEmail: %v)", err)
		}

		u := seedUser(t, s, auth.User{Email: "ed@example.com", Name: "Ed", Active: true})
		changed := *u
		changed.Role = auth.RoleAdmin
		if err := s.Save(ctx, auth.UserSave{User: &changed, Permissions: bad, NewPasswordHash: "new-hash"}); err == nil {
			t.Fatal("Save accepted a grant longer than its column")
		}
		got, err := s.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Role != auth.RoleEditor || got.PasswordHash != u.PasswordHash || got.SessionEpoch != u.SessionEpoch {
			t.Errorf("a failed update was half-applied: role %q, password changed %v, epoch %d",
				got.Role, got.PasswordHash != u.PasswordHash, got.SessionEpoch)
		}
	})
}

// Two superadmins demoting each other at the same moment, or deleting
// each other: each change on its own leaves the other, so a check made
// before writing passes for both. Decided under the lock, the second sees
// the first and is refused — the site always keeps a superadmin.
func TestTheLastSuperadminSurvivesConcurrentChanges(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)

		for round := range 5 {
			for _, how := range []string{"demote", "delete"} {
				dbtest.Truncate(t, db)
				a := seedUser(t, s, auth.User{Email: "a@example.com", Name: "A", Role: auth.RoleSuperadmin, Active: true})
				b := seedUser(t, s, auth.User{Email: "b@example.com", Name: "B", Role: auth.RoleSuperadmin, Active: true})

				change := func(u *auth.User) error {
					if how == "delete" {
						return s.Delete(ctx, u.ID)
					}
					demoted := *u
					demoted.Role = auth.RoleAdmin
					return s.Save(ctx, auth.UserSave{User: &demoted})
				}
				var wg sync.WaitGroup
				start := make(chan struct{})
				errs := make([]error, 2)
				for i, u := range []*auth.User{a, b} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						errs[i] = change(u)
					}()
				}
				close(start)
				wg.Wait()

				var refused int
				for _, err := range errs {
					switch {
					case errors.Is(err, auth.ErrLastSuperadmin):
						refused++
					case err != nil:
						t.Fatalf("round %d, %s: unexpected error %v", round, how, err)
					}
				}
				if refused != 1 {
					t.Errorf("round %d, %s: %d of 2 changes refused, want exactly 1", round, how, refused)
				}
				if n, err := s.CountOtherActiveSuperadmins(ctx, 0); err != nil || n != 1 {
					t.Fatalf("round %d, %s: %d active superadmins left (err %v), want 1", round, how, n, err)
				}
			}
		}
	})
}
