// Package auth provides the CMS's user accounts: password hashing, the
// Postgres-backed user store, roles, and login throttling.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/tsawler/cms/internal/dberr"
	"github.com/tsawler/cms/internal/sqldb"
)

// Role controls what a user may do in the admin area.
type Role string

const (
	// RoleSuperadmin has every admin power plus raw HTML access in the
	// in-place editor (the whole-page source view) — for users who are
	// comfortable hand-editing markup.
	RoleSuperadmin Role = "superadmin"
	// RoleAdmin may manage users and site settings in addition to content.
	RoleAdmin Role = "admin"
	// RoleEditor may create and edit content but not manage users.
	RoleEditor Role = "editor"
)

// Valid reports whether r is a role the CMS knows about.
func (r Role) Valid() bool { return r == RoleSuperadmin || r == RoleAdmin || r == RoleEditor }

// IsAdmin reports whether the role carries admin powers (user management,
// unsanitized content, page CSS/JS). Superadmin is a superset of admin.
func (r Role) IsAdmin() bool { return r == RoleAdmin || r == RoleSuperadmin }

// IsSuperadmin reports whether the role may edit raw page HTML in the
// in-place editor.
func (r Role) IsSuperadmin() bool { return r == RoleSuperadmin }

// User is a CMS account.
type User struct {
	ID           int64
	Email        string
	Name         string
	PasswordHash string
	Role         Role
	Active       bool
	// Permissions are the user's grants, loaded by GetByID and
	// GetByEmail (All leaves it nil — the users list doesn't need
	// them). Meaningful for editors only; admin roles pass every
	// Can check regardless of what is stored here.
	Permissions []Permission
	// TOTPSecret is the base32 key an authenticator app was enrolled
	// with, empty when two-factor is off. TOTPLastStep is the time step
	// of the last accepted code; see Store.ConsumeTOTPStep.
	TOTPSecret   string
	TOTPLastStep int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

var (
	// ErrNotFound is returned when no user matches the query.
	ErrNotFound = errors.New("auth: user not found")
	// ErrDuplicateEmail is returned by Insert/Update when the email is taken.
	ErrDuplicateEmail = errors.New("auth: email already in use")
	// ErrInvalidCredentials is returned by Authenticate for a bad email or
	// password, or an inactive account. It deliberately does not say which.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
)

// Store reads and writes users in Postgres.
type Store struct {
	db *sqldb.DB
	// logger reports work that happens alongside a request rather than
	// as its result — currently the password rehash in Authenticate,
	// whose failure must not fail the login. Defaults to slog.Default();
	// the CMS points it at the configured logger with SetLogger.
	logger *slog.Logger
}

// NewStore returns a Store backed by db.
func NewStore(db *sqldb.DB) *Store {
	return &Store{db: db, logger: slog.Default()}
}

// SetLogger directs the store's background reporting at l.
func (s *Store) SetLogger(l *slog.Logger) {
	if l != nil {
		s.logger = l
	}
}

const userColumns = "id, email, name, password_hash, role, active, totp_secret, totp_last_step, created_at, updated_at"

func scanUser(row sqldb.Scanner) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.Active, &u.TOTPSecret, &u.TOTPLastStep, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByID returns the user with the given id, grants included, or
// ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id int64) (*User, error) {
	row := s.db.QueryRow(ctx, "SELECT "+userColumns+" FROM cms_users WHERE id = $1", id)
	u, err := scanUser(row)
	if err != nil {
		return nil, err
	}
	if err := s.loadPermissions(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// GetByEmail returns the user with the given email (case-insensitive),
// grants included, or ErrNotFound.
func (s *Store) GetByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRow(ctx, "SELECT "+userColumns+" FROM cms_users WHERE email = $1",
		strings.ToLower(strings.TrimSpace(email)))
	u, err := scanUser(row)
	if err != nil {
		return nil, err
	}
	if err := s.loadPermissions(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// All returns every user, ordered by name then email.
func (s *Store) All(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, "SELECT "+userColumns+" FROM cms_users ORDER BY name, email")
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (User, error) {
		u, err := scanUser(row)
		if err != nil {
			return User{}, err
		}
		return *u, nil
	})
}

// Count returns the total number of users.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, "SELECT count(*) FROM cms_users").Scan(&n)
	return n, err
}

// Insert stores a new user and returns its id. Email is normalized to lower
// case. Returns ErrDuplicateEmail if the address is taken.
func (s *Store) Insert(ctx context.Context, u *User) (int64, error) {
	id, err := s.db.InsertID(ctx, `
		INSERT INTO cms_users (email, name, password_hash, role, active)
		VALUES ($1, $2, $3, $4, $5)`,
		strings.ToLower(strings.TrimSpace(u.Email)), u.Name, u.PasswordHash, u.Role, u.Active)
	if dberr.IsUniqueViolation(err) {
		return 0, ErrDuplicateEmail
	}
	if err != nil {
		return 0, err
	}
	u.ID = id
	return u.ID, nil
}

// Update saves email, name, role, and active for an existing user. It does
// not touch the password; use UpdatePassword for that.
func (s *Store) Update(ctx context.Context, u *User) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE cms_users
		SET email = $1, name = $2, role = $3, active = $4, updated_at = now()
		WHERE id = $5`,
		strings.ToLower(strings.TrimSpace(u.Email)), u.Name, u.Role, u.Active, u.ID)
	if dberr.IsUniqueViolation(err) {
		return ErrDuplicateEmail
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePassword replaces a user's password hash.
func (s *Store) UpdatePassword(ctx context.Context, id int64, passwordHash string) error {
	tag, err := s.db.Exec(ctx,
		"UPDATE cms_users SET password_hash = $1, updated_at = now() WHERE id = $2",
		passwordHash, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate checks email and password and returns the matching active
// user, or ErrInvalidCredentials. To resist timing probes for valid
// addresses, it verifies a dummy hash when the email is unknown.
func (s *Store) Authenticate(ctx context.Context, email, password string) (*User, error) {
	u, err := s.GetByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		_, _ = VerifyPassword(password, dummyHash)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	ok, err := VerifyPassword(password, u.PasswordHash)
	if err != nil || !ok || !u.Active {
		return nil, ErrInvalidCredentials
	}
	s.rehash(ctx, u, password)
	return u, nil
}

// rehash re-derives a user's stored hash at the current algorithm and
// cost, now that a correct password has proved itself — the one moment
// the CMS holds a plaintext to derive from. This is how accounts
// imported from a bcrypt system become argon2id: one login at a time,
// with no password reset asked of anybody. A user who never logs in
// again simply keeps a bcrypt hash, which still verifies.
//
// A failure here is not a failed login. The stored hash is still the
// right one, and the next login will try the upgrade again, so we say so
// in the log and let the caller through.
func (s *Store) rehash(ctx context.Context, u *User, password string) {
	if !NeedsRehash(u.PasswordHash) {
		return
	}
	hash, err := HashPassword(password)
	if err != nil {
		s.logger.Error("auth: rehashing password", "user", u.Email, "err", err)
		return
	}
	swapped, err := s.swapPasswordHash(ctx, u.ID, u.PasswordHash, hash)
	if err != nil {
		s.logger.Error("auth: storing rehashed password", "user", u.Email, "err", err)
		return
	}
	if !swapped {
		// The row moved under us: a password change or a concurrent login
		// got there first. Either way the stored hash is now somebody
		// else's to worry about, and ours would be stale.
		return
	}
	u.PasswordHash = hash
	s.logger.Info("auth: upgraded password hash to argon2id", "user", u.Email)
}

// swapPasswordHash replaces a password hash only if the stored value is
// still the one we verified against, reporting whether it landed. The
// compare-and-set is what keeps a rehash from clobbering a password
// changed in another tab while the login was in flight: it can only ever
// overwrite the exact hash it re-derived.
//
// Unlike UpdatePassword this leaves updated_at alone. Re-deriving a hash
// from an unchanged password is not an edit to the account, and logging
// in should not make every user look freshly modified.
func (s *Store) swapPasswordHash(ctx context.Context, id int64, oldHash, newHash string) (bool, error) {
	tag, err := s.db.Exec(ctx,
		"UPDATE cms_users SET password_hash = $1 WHERE id = $2 AND password_hash = $3",
		newHash, id, oldHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// dummyHash is a hash of no particular password, verified when an unknown
// email is submitted so that known and unknown addresses take the same time.
const dummyHash = "$argon2id$v=19$m=65536,t=1,p=4$AAAAAAAAAAAAAAAAAAAAAA$t8VtC1hK9dQ3yLxkzPPmm9jTKQJyPmNkAkD6xhhrLPM"
