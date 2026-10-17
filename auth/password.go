package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
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

// argonSlots bounds how many argon2id derivations run at once. Each one
// allocates argonMemory — 64 MiB — for as long as it runs, and a login
// form takes as many concurrent POSTs as anyone cares to send, every one
// of them a derivation whether or not the address exists (the dummy hash
// in Authenticate makes sure of it). Unbounded, a few hundred at once is
// tens of gigabytes. Each derivation already keeps argonThreads cores
// busy, so more slots than cores/argonThreads buys no throughput, only
// memory; the floor of two keeps one slow login from stalling the rest.
var argonSlots = make(chan struct{}, max(2, runtime.NumCPU()/argonThreads))

// idKey is argon2.IDKey under one of the argonSlots, waiting for a slot
// until ctx ends.
func idKey(ctx context.Context, password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) ([]byte, error) {
	select {
	case argonSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-argonSlots }()
	return argon2.IDKey(password, salt, time, memory, threads, keyLen), nil
}

// ErrInvalidHash is returned by VerifyPassword when the stored hash is
// neither a well-formed argon2id PHC string nor a usable bcrypt hash.
var ErrInvalidHash = errors.New("auth: stored password hash is malformed")

// HashPassword derives an argon2id hash of password and returns it in PHC
// string format, e.g. $argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>. Every
// hash this CMS writes — new accounts, password changes, resets — is
// argon2id; bcrypt is verified but never issued.
//
// It waits for a free derivation slot if all of them are busy; see
// argonSlots.
func HashPassword(password string) (string, error) {
	return hashPassword(context.Background(), password)
}

// hashPassword is HashPassword, giving up on a derivation slot when ctx
// ends.
func hashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating salt: %w", err)
	}
	key, err := idKey(ctx, []byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	if err != nil {
		return "", err
	}
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
//
// Like HashPassword it waits for a free derivation slot; see argonSlots.
func VerifyPassword(password, hash string) (bool, error) {
	return verifyPassword(context.Background(), password, hash)
}

// verifyPassword is VerifyPassword, giving up on a derivation slot when
// ctx ends — the client has gone, so nobody is waiting for the answer.
func verifyPassword(ctx context.Context, password, hash string) (bool, error) {
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
	got, err := idKey(ctx, []byte(password), salt, p.time, p.memory, p.threads, uint32(len(want)))
	if err != nil {
		return false, err
	}
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
