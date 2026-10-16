package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("expected match, got ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Fatal("wrong password verified as correct")
	}
}

func TestHashPasswordUniqueSalts(t *testing.T) {
	h1, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("two hashes of the same password are identical; salt is not random")
	}
}

// bcryptHash is a real bcrypt hash of bcryptPassword at cost 10, of the
// kind a site being migrated from another system arrives with. It is a
// fixture rather than something generated in the test: the point is that
// we verify hashes we did not produce.
const (
	bcryptPassword = "correct horse battery staple"
	bcryptHash     = "$2a$10$WxlFvWuIR1RNFKqFfiYsqu8Ny86o4K2IBeRHieCuS8OsFyZQ75jXy"
)

func TestVerifyPasswordBcrypt(t *testing.T) {
	// $2a$, $2b$, and $2y$ differ only in which implementation wrote the
	// hash; the bytes after the prefix mean the same thing in all three.
	for _, prefix := range []string{"$2a$", "$2b$", "$2y$"} {
		hash := prefix + strings.TrimPrefix(bcryptHash, "$2a$")

		ok, err := VerifyPassword(bcryptPassword, hash)
		if err != nil || !ok {
			t.Errorf("VerifyPassword(correct, %s…) = %v, %v; want true, nil", prefix, ok, err)
		}

		ok, err = VerifyPassword("wrong password", hash)
		if err != nil {
			t.Errorf("VerifyPassword(wrong, %s…): %v", prefix, err)
		}
		if ok {
			t.Errorf("wrong password verified against %s… hash", prefix)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	current, err := HashPassword("whatever")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		hash string
		want bool
	}{
		{"freshly hashed", current, false},
		{"bcrypt", bcryptHash, true},
		{"weaker argon2id parameters", "$argon2id$v=19$m=4096,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$t8VtC1hK9dQ3yLxkzPPmm9jTKQJyPmNkAkD6xhhrLPM", true},
		{"short derived key", "$argon2id$v=19$m=65536,t=1,p=4$AAAAAAAAAAAAAAAAAAAAAA$t8VtC1hK9dQ3yLxkzPPmm9g", true},
		{"malformed", "not a hash", true},
	}
	for _, tc := range cases {
		if got := NeedsRehash(tc.hash); got != tc.want {
			t.Errorf("NeedsRehash(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestVerifyPasswordMalformedHash(t *testing.T) {
	for _, bad := range []string{
		"",
		"not a hash",
		"$argon2i$v=19$m=65536,t=1,p=4$AAAA$BBBB",
		"$argon2id$v=19$m=65536,t=1,p=4$!!!!$BBBB",
		// Claims to be bcrypt, but there is no hash after the cost.
		"$2a$10$",
		"$2b$10$WxlFvWuIR1RNFKqFfiYsqu",
	} {
		if _, err := VerifyPassword("x", bad); err == nil {
			t.Errorf("expected error for malformed hash %q", bad)
		}
	}
}

func TestDummyHashIsWellFormed(t *testing.T) {
	ok, err := VerifyPassword("anything", dummyHash)
	if err != nil {
		t.Fatalf("dummyHash is malformed: %v", err)
	}
	if ok {
		t.Fatal("dummyHash unexpectedly matches a password")
	}
}

// Every argon2id derivation holds 64 MiB while it runs, so they share a
// fixed number of slots. With every slot taken, a check waits — and gives
// up when its request does, rather than piling up behind the others.
func TestPasswordChecksWaitForASlot(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	for range cap(argonSlots) {
		argonSlots <- struct{}{}
	}
	held := cap(argonSlots)
	release := func() {
		for ; held > 0; held-- {
			<-argonSlots
		}
	}
	t.Cleanup(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := verifyPassword(ctx, "correct horse", hash); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("verify with every slot held: err = %v, want context.DeadlineExceeded", err)
	}
	if _, err := hashPassword(ctx, "correct horse"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("hash with every slot held: err = %v, want context.DeadlineExceeded", err)
	}

	// A freed slot lets the waiting check through, and it gives the slot
	// back when done.
	done := make(chan bool)
	go func() {
		ok, _ := VerifyPassword("correct horse", hash)
		done <- ok
	}()
	select {
	case <-done:
		t.Fatal("a check ran with every slot held")
	case <-time.After(20 * time.Millisecond):
	}
	<-argonSlots
	held--
	if ok := <-done; !ok {
		t.Error("the check that waited for a slot got the wrong answer")
	}
	if len(argonSlots) != held {
		t.Errorf("%d slots held after the check finished, want %d", len(argonSlots), held)
	}
}
