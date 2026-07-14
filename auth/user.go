// Package auth provides the CMS's user accounts: password hashing, the
// Postgres-backed user store, roles, and login throttling.
package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Role controls what a user may do in the admin area.
type Role string

const (
	// RoleAdmin may manage users and site settings in addition to content.
	RoleAdmin Role = "admin"
	// RoleEditor may create and edit content but not manage users.
	RoleEditor Role = "editor"
)

// Valid reports whether r is a role the CMS knows about.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleEditor }

// User is a CMS account.
type User struct {
	ID           int64
	Email        string
	Name         string
	PasswordHash string
	Role         Role
	Active       bool
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
	db *pgxpool.Pool
}

// NewStore returns a Store backed by db.
func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

const userColumns = "id, email, name, password_hash, role, active, created_at, updated_at"

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.Active, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByID returns the user with the given id, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id int64) (*User, error) {
	row := s.db.QueryRow(ctx, "SELECT "+userColumns+" FROM cms_users WHERE id = $1", id)
	return scanUser(row)
}

// GetByEmail returns the user with the given email (case-insensitive), or
// ErrNotFound.
func (s *Store) GetByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRow(ctx, "SELECT "+userColumns+" FROM cms_users WHERE email = $1",
		strings.ToLower(strings.TrimSpace(email)))
	return scanUser(row)
}

// All returns every user, ordered by name then email.
func (s *Store) All(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, "SELECT "+userColumns+" FROM cms_users ORDER BY name, email")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (User, error) {
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
	err := s.db.QueryRow(ctx, `
		INSERT INTO cms_users (email, name, password_hash, role, active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		strings.ToLower(strings.TrimSpace(u.Email)), u.Name, u.PasswordHash, u.Role, u.Active,
	).Scan(&u.ID)
	if isUniqueViolation(err) {
		return 0, ErrDuplicateEmail
	}
	return u.ID, err
}

// Update saves email, name, role, and active for an existing user. It does
// not touch the password; use UpdatePassword for that.
func (s *Store) Update(ctx context.Context, u *User) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE cms_users
		SET email = $1, name = $2, role = $3, active = $4, updated_at = now()
		WHERE id = $5`,
		strings.ToLower(strings.TrimSpace(u.Email)), u.Name, u.Role, u.Active, u.ID)
	if isUniqueViolation(err) {
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
	return u, nil
}

// dummyHash is a hash of no particular password, verified when an unknown
// email is submitted so that known and unknown addresses take the same time.
const dummyHash = "$argon2id$v=19$m=65536,t=1,p=4$AAAAAAAAAAAAAAAAAAAAAA$t8VtC1hK9dQ3yLxkzPPmm9jTKQJyPmNkAkD6xhhrLPM"

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
