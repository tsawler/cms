package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// argon2id parameters. These follow the argon2 package's recommended
// interactive-login defaults.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrInvalidHash is returned by VerifyPassword when the stored hash is
// neither a well-formed argon2id PHC string nor a usable bcrypt hash.
var ErrInvalidHash = errors.New("auth: stored password hash is malformed")

// HashPassword derives an argon2id hash of password and returns it in PHC
// string format, e.g. $argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>. Every
// hash this CMS writes — new accounts, password changes, resets — is
// argon2id; bcrypt is verified but never issued.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches the stored hash, which
// may be either argon2id or bcrypt. Stored hashes are self-describing —
// argon2id PHC strings open with "$argon2id$", bcrypt's with "$2a$",
// "$2b$", or "$2y$" — so a site carrying accounts imported from a
// bcrypt system can verify both while it migrates. See NeedsRehash for
// the other half of that migration.
//
// The argon2id comparison is constant time in the derived key; bcrypt's
// is constant time in the package.
func VerifyPassword(password, hash string) (bool, error) {
	if isBcryptHash(hash) {
		err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
			return false, nil
		default:
			// Truncated, corrupt, or an unsupported cost: the value in
			// the column is not something we can check a password against.
			return false, ErrInvalidHash
		}
	}

	p, salt, want, err := parseArgon2id(hash)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether a stored hash should be replaced the next
// time we hold the plaintext that goes with it — because it is bcrypt
// (an imported account we have not migrated yet) or argon2id at cost
// parameters we have since moved off.
//
// A malformed hash also reports true, which costs nothing: the only
// caller rehashes after a successful verification, and nothing verifies
// against a hash we cannot parse.
func NeedsRehash(hash string) bool {
	p, _, key, err := parseArgon2id(hash)
	if err != nil {
		return true
	}
	return p != currentParams || len(key) != argonKeyLen
}

// isBcryptHash reports whether hash carries one of bcrypt's version
// prefixes. $2a$ is the original, $2y$ the PHP fix for the 2011 sign
// bug, $2b$ the OpenBSD one; all three are hashes x/crypto verifies.
// The obsolete $2$ is deliberately absent — x/crypto rejects it, so
// treating it as bcrypt would only trade one error for another.
func isBcryptHash(hash string) bool {
	return strings.HasPrefix(hash, "$2a$") ||
		strings.HasPrefix(hash, "$2b$") ||
		strings.HasPrefix(hash, "$2y$")
}

// argonParams are the cost parameters encoded in a PHC string. Comparing
// a parsed set against currentParams is what tells NeedsRehash that a
// hash predates a change to the constants above.
type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

var currentParams = argonParams{memory: argonMemory, time: argonTime, threads: argonThreads}

// parseArgon2id splits a PHC string into its parameters, salt, and
// derived key. It returns ErrInvalidHash for anything else, including a
// bcrypt hash — callers that accept both check for bcrypt first.
func parseArgon2id(phc string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	return p, salt, key, nil
}
