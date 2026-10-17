// Package auth provides the CMS's user accounts: password hashing, the
// Postgres-backed user store, roles, and login throttling.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/tsawler/cms/internal/dberr"
	"github.com/tsawler/cms/internal/sqldb"
)

// Role controls what a user may do in the admin area.
type Role string

const (
	// RoleSuperadmin has every admin power plus snippet management, the
	// admin panel's Pages section, and unlisted page templates.
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

// IsSuperadmin reports whether the role carries the superadmin-only
// powers (snippet management, the Pages section, unlisted templates).
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
	// SessionEpoch counts changes to how the account signs in — its
	// password, its two-factor enrollment. A session is granted under the
	// epoch current at login and honoured only while it still is, so a
	// change ends every session the account had (see sessiondata.KeyEpoch).
	SessionEpoch int64
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
	// ErrLastSuperadmin is returned by Save and Delete for a change that
	// would leave the site with no active superadmin.
	ErrLastSuperadmin = errors.New("auth: the last active superadmin cannot lose the role")
)

// handle is what the user writes run against: the pool, or a
// transaction that several of them share.
type handle interface {
	Exec(ctx context.Context, query string, args ...any) (sqldb.Result, error)
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
	InsertID(ctx context.Context, query string, args ...any) (int64, error)
}

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

const userColumns = "id, email, name, password_hash, role, active, totp_secret, totp_last_step, session_epoch, created_at, updated_at"

func scanUser(row sqldb.Scanner) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.Active, &u.TOTPSecret, &u.TOTPLastStep, &u.SessionEpoch, &u.CreatedAt, &u.UpdatedAt)
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

// UserFilter narrows Filtered and CountFiltered. The zero value matches
// every account.
type UserFilter struct {
	// Query keeps accounts whose name or email contains it,
	// case-insensitively.
	Query string
	// Active, when set, keeps only accounts whose Active flag matches it.
	Active *bool
}

// filterWhere renders f as a WHERE clause ("" when f is empty) plus its
// arguments. Shared by Filtered and CountFiltered so the count always
// describes the same list the window is read from.
func (s *Store) filterWhere(f UserFilter) (string, []any) {
	var where []string
	var args []any
	if f.Query != "" {
		args = append(args, "%"+sqldb.EscapeLike(f.Query)+"%")
		p := "$" + strconv.Itoa(len(args))
		d := s.db.Dialect()
		where = append(where, "("+d.CaseInsensitiveLike("name", p)+" OR "+d.CaseInsensitiveLike("email", p)+")")
	}
	if f.Active != nil {
		args = append(args, *f.Active)
		where = append(where, "active = $"+strconv.Itoa(len(args)))
	}
	if len(where) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

// Filtered returns one window of the users matching f, ordered by name
// then email — the query behind the admin's paginated users list. Like
// All it leaves Permissions unloaded.
func (s *Store) Filtered(ctx context.Context, f UserFilter, limit, offset int) ([]User, error) {
	where, args := s.filterWhere(f)
	args = append(args, limit, offset)
	n := len(args)
	rows, err := s.db.Query(ctx, "SELECT "+userColumns+" FROM cms_users"+where+
		" ORDER BY name, email LIMIT $"+strconv.Itoa(n-1)+" OFFSET $"+strconv.Itoa(n), args...)
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

// CountFiltered returns how many users match f — the total Filtered
// pages through.
func (s *Store) CountFiltered(ctx context.Context, f UserFilter) (int, error) {
	where, args := s.filterWhere(f)
	var n int
	err := s.db.QueryRow(ctx, "SELECT count(*) FROM cms_users"+where, args...).Scan(&n)
	return n, err
}

// CountOtherActiveSuperadmins returns how many active superadmins the
// site has besides the account with excludeID.
//
// It answers one question: would this change leave nobody able to do the
// superadmin-only things? Snippets, the Pages section, masquerade, and
// the site lock are all superadmin-gated, and only a superadmin can grant
// the role — so a site that reaches zero has no way back except an edit
// straight to the database. See admin.server.lastActiveSuperadmin.
func (s *Store) CountOtherActiveSuperadmins(ctx context.Context, excludeID int64) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		"SELECT count(*) FROM cms_users WHERE role = $1 AND active AND id <> $2",
		RoleSuperadmin, excludeID).Scan(&n)
	return n, err
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
	return insertUser(ctx, s.db, u)
}

func insertUser(ctx context.Context, h handle, u *User) (int64, error) {
	id, err := h.InsertID(ctx, `
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
	return updateUser(ctx, s.db, u)
}

func updateUser(ctx context.Context, h handle, u *User) error {
	tag, err := h.Exec(ctx, `
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

// Delete removes a user outright. The schema does the bookkeeping:
// grants and password-reset tokens are dropped with the row, while media
// uploads and posts survive with their user reference nulled. Returns
// ErrNotFound when no such user exists.
//
// Deleting the last active superadmin is refused with ErrLastSuperadmin,
// decided under the lock keepSuperadmin takes, so two superadmins
// deleting each other at the same moment cannot both succeed.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := keepSuperadmin(ctx, tx, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "DELETE FROM cms_users WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// keepSuperadmin refuses, with ErrLastSuperadmin, a change that takes id
// out of the active superadmins when it is the only one. It locks every
// active superadmin row, in id order, before counting: two such changes
// at once then queue on the same locks, and the second counts after the
// first has committed. Counting without the lock let two superadmins
// demote — or delete — each other at the same moment, each seeing the
// other still there, and leave a site nobody can administer.
func keepSuperadmin(ctx context.Context, tx *sqldb.Tx, id int64) error {
	rows, err := tx.Query(ctx,
		"SELECT id FROM cms_users WHERE role = $1 AND active ORDER BY id FOR UPDATE", RoleSuperadmin)
	if err != nil {
		return err
	}
	ids, err := sqldb.CollectRows(rows, func(row sqldb.Scanner) (int64, error) {
		var v int64
		err := row.Scan(&v)
		return v, err
	})
	if err != nil {
		return err
	}
	if len(ids) == 1 && ids[0] == id {
		return ErrLastSuperadmin
	}
	return nil
}

// UserSave is one save of the admin's user form, which Save applies as a
// whole or not at all.
type UserSave struct {
	// User is the account: ID zero creates it, and then PasswordHash on
	// it is the new account's password. Otherwise its email, name, role
	// and active flag are written over the stored ones.
	User *User
	// Permissions replaces the account's grants.
	Permissions []Permission
	// NewPasswordHash, on an update, replaces the password; empty keeps
	// it. Like UpdatePassword it moves the account to a new SessionEpoch.
	NewPasswordHash string
	// DisableTOTP, on an update, turns two-factor off, as DisableTOTP does.
	DisableTOTP bool
}

// Save applies a user-form save in one transaction. Done as separate
// writes, a failure part-way left half a save: an account created with
// no grants (and an "email already in use" on the retry), or a role
// changed while the password the same form set was not.
//
// An update that ends the account's standing as an active superadmin is
// refused with ErrLastSuperadmin when it is the last one; see
// keepSuperadmin. Insert's and Update's errors carry through as theirs.
func (s *Store) Save(ctx context.Context, in UserSave) error {
	u := in.User
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if u.ID == 0 {
		if _, err := insertUser(ctx, tx, u); err != nil {
			return err
		}
	} else {
		if u.Role != RoleSuperadmin || !u.Active {
			if err := keepSuperadmin(ctx, tx, u.ID); err != nil {
				return err
			}
		}
		if err := updateUser(ctx, tx, u); err != nil {
			return err
		}
		if in.NewPasswordHash != "" {
			if err := setPassword(ctx, tx, u.ID, in.NewPasswordHash); err != nil {
				return err
			}
		}
		if in.DisableTOTP {
			if err := disableTOTP(ctx, tx, u.ID); err != nil {
				return err
			}
		}
	}
	if err := replacePermissions(ctx, tx, u.ID, in.Permissions); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdatePassword replaces a user's password hash, and moves the account
// to a new SessionEpoch: every session it already has stops working.
func (s *Store) UpdatePassword(ctx context.Context, id int64, passwordHash string) error {
	return setPassword(ctx, s.db, id, passwordHash)
}

func setPassword(ctx context.Context, h handle, id int64, passwordHash string) error {
	tag, err := h.Exec(ctx,
		"UPDATE cms_users SET password_hash = $1, session_epoch = session_epoch + 1, updated_at = now() WHERE id = $2",
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
		// The dummy only buys time; its answer is ignored. A request
		// that gave up waiting for a derivation slot reports that
		// rather than a wrong password.
		_, _ = verifyPassword(ctx, password, dummyHash)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	ok, err := verifyPassword(ctx, password, u.PasswordHash)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
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
	hash, err := hashPassword(ctx, password)
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
