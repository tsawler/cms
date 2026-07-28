package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
)

// seedUser inserts a user with sensible defaults, returning it with its id
// filled in. Tests that only care about one field can override it and leave
// the rest alone.
func seedUser(t *testing.T, s *auth.Store, u auth.User) *auth.User {
	t.Helper()
	if u.Email == "" {
		u.Email = "user@example.com"
	}
	if u.Role == "" {
		u.Role = auth.RoleEditor
	}
	if u.PasswordHash == "" {
		hash, err := auth.HashPassword("password123")
		if err != nil {
			t.Fatalf("hashing password: %v", err)
		}
		u.PasswordHash = hash
	}
	if _, err := s.Insert(context.Background(), &u); err != nil {
		t.Fatalf("seeding user %q: %v", u.Email, err)
	}
	return &u
}

func TestUserInsertAndGet(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)

		want := seedUser(t, s, auth.User{
			Email:  "Ada@Example.com",
			Name:   "Ada Lovelace",
			Role:   auth.RoleAdmin,
			Active: true,
		})
		if want.ID == 0 {
			t.Fatal("Insert did not set the user's id")
		}

		got, err := s.GetByID(ctx, want.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		// Insert normalizes the address, so the stored value is lower case
		// even though the caller supplied mixed case.
		if got.Email != "ada@example.com" {
			t.Errorf("email = %q, want %q", got.Email, "ada@example.com")
		}
		if got.Name != "Ada Lovelace" {
			t.Errorf("name = %q, want %q", got.Name, "Ada Lovelace")
		}
		if got.Role != auth.RoleAdmin {
			t.Errorf("role = %q, want %q", got.Role, auth.RoleAdmin)
		}
		if !got.Active {
			t.Error("active = false, want true")
		}
		if got.CreatedAt.IsZero() {
			t.Error("created_at was not defaulted by the database")
		}
	})
}

func TestUserGetByEmailNormalizes(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		want := seedUser(t, s, auth.User{Email: "grace@example.com", Active: true})

		// Lookup normalizes case and surrounding space the same way Insert
		// does, so these all have to find the same row.
		for _, probe := range []string{"grace@example.com", "GRACE@EXAMPLE.COM", "  Grace@Example.com  "} {
			got, err := s.GetByEmail(ctx, probe)
			if err != nil {
				t.Fatalf("GetByEmail(%q): %v", probe, err)
			}
			if got.ID != want.ID {
				t.Errorf("GetByEmail(%q) = id %d, want %d", probe, got.ID, want.ID)
			}
		}
	})
}

func TestUserGetNotFound(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)

		if _, err := s.GetByID(ctx, 4242); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("GetByID(missing) = %v, want ErrNotFound", err)
		}
		if _, err := s.GetByEmail(ctx, "nobody@example.com"); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("GetByEmail(missing) = %v, want ErrNotFound", err)
		}
	})
}

func TestUserInsertDuplicateEmail(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		seedUser(t, s, auth.User{Email: "dup@example.com", Active: true})

		// The second insert differs only in case, which normalization folds
		// onto the first — this is the unique-violation mapping under test.
		second := auth.User{Email: "DUP@example.com", PasswordHash: "x", Role: auth.RoleEditor, Active: true}
		_, err := s.Insert(ctx, &second)
		if !errors.Is(err, auth.ErrDuplicateEmail) {
			t.Fatalf("Insert(duplicate) = %v, want ErrDuplicateEmail", err)
		}
	})
}

func TestUserAllAndCount(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)

		if n, err := s.Count(ctx); err != nil || n != 0 {
			t.Fatalf("Count(empty) = %d, %v, want 0, nil", n, err)
		}

		seedUser(t, s, auth.User{Email: "c@example.com", Name: "Carol", Active: true})
		seedUser(t, s, auth.User{Email: "a@example.com", Name: "Alice", Active: true})
		seedUser(t, s, auth.User{Email: "b@example.com", Name: "Bob", Active: true})

		n, err := s.Count(ctx)
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n != 3 {
			t.Errorf("Count = %d, want 3", n)
		}

		users, err := s.All(ctx)
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		gotNames := make([]string, len(users))
		for i, u := range users {
			gotNames[i] = u.Name
		}
		want := []string{"Alice", "Bob", "Carol"}
		if len(gotNames) != len(want) {
			t.Fatalf("All returned %d users, want %d", len(gotNames), len(want))
		}
		for i := range want {
			if gotNames[i] != want[i] {
				t.Errorf("All order = %v, want %v", gotNames, want)
				break
			}
		}
	})
}

func TestUserUpdate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		u := seedUser(t, s, auth.User{Email: "before@example.com", Name: "Before", Active: true})
		originalHash := u.PasswordHash

		u.Email = "After@example.com"
		u.Name = "After"
		u.Role = auth.RoleSuperadmin
		u.Active = false
		if err := s.Update(ctx, u); err != nil {
			t.Fatalf("Update: %v", err)
		}

		got, err := s.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Email != "after@example.com" {
			t.Errorf("email = %q, want %q", got.Email, "after@example.com")
		}
		if got.Name != "After" {
			t.Errorf("name = %q, want %q", got.Name, "After")
		}
		if got.Role != auth.RoleSuperadmin {
			t.Errorf("role = %q, want %q", got.Role, auth.RoleSuperadmin)
		}
		if got.Active {
			t.Error("active = true, want false")
		}
		// Update documents that it leaves the password alone.
		if got.PasswordHash != originalHash {
			t.Error("Update changed the password hash, want it untouched")
		}
	})
}

func TestUserUpdateMissing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)

		err := s.Update(ctx, &auth.User{ID: 4242, Email: "ghost@example.com", Role: auth.RoleEditor})
		if !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("Update(missing) = %v, want ErrNotFound", err)
		}
		if err := s.UpdatePassword(ctx, 4242, "x"); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("UpdatePassword(missing) = %v, want ErrNotFound", err)
		}
	})
}

func TestUserUpdateDuplicateEmail(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		seedUser(t, s, auth.User{Email: "taken@example.com", Active: true})
		mover := seedUser(t, s, auth.User{Email: "mover@example.com", Active: true})

		mover.Email = "taken@example.com"
		if err := s.Update(ctx, mover); !errors.Is(err, auth.ErrDuplicateEmail) {
			t.Errorf("Update(duplicate) = %v, want ErrDuplicateEmail", err)
		}
	})
}

func TestUserUpdatePasswordAndAuthenticate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		u := seedUser(t, s, auth.User{Email: "login@example.com", Active: true})

		if _, err := s.Authenticate(ctx, "login@example.com", "password123"); err != nil {
			t.Fatalf("Authenticate(correct) = %v, want success", err)
		}

		next, err := auth.HashPassword("newsecret")
		if err != nil {
			t.Fatalf("hashing: %v", err)
		}
		if err := s.UpdatePassword(ctx, u.ID, next); err != nil {
			t.Fatalf("UpdatePassword: %v", err)
		}

		if _, err := s.Authenticate(ctx, "login@example.com", "newsecret"); err != nil {
			t.Errorf("Authenticate(new password) = %v, want success", err)
		}
		if _, err := s.Authenticate(ctx, "login@example.com", "password123"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("Authenticate(old password) = %v, want ErrInvalidCredentials", err)
		}
	})
}

func TestUserAuthenticateRejects(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := auth.NewStore(db)
		seedUser(t, s, auth.User{Email: "active@example.com", Active: true})
		seedUser(t, s, auth.User{Email: "inactive@example.com", Active: false})

		cases := []struct {
			name, email, password string
		}{
			{"wrong password", "active@example.com", "nope"},
			{"unknown email", "ghost@example.com", "password123"},
			{"inactive account", "inactive@example.com", "password123"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// Every rejection reports the same error, so a caller cannot
				// tell a bad password from an unknown or disabled account.
				if _, err := s.Authenticate(ctx, tc.email, tc.password); !errors.Is(err, auth.ErrInvalidCredentials) {
					t.Errorf("Authenticate = %v, want ErrInvalidCredentials", err)
				}
			})
		}
	})
}
