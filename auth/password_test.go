package auth

import (
	"strings"
	"testing"
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

func TestVerifyPasswordMalformedHash(t *testing.T) {
	for _, bad := range []string{
		"",
		"not a hash",
		"$argon2i$v=19$m=65536,t=1,p=4$AAAA$BBBB",
		"$argon2id$v=19$m=65536,t=1,p=4$!!!!$BBBB",
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
