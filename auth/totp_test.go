package auth

import (
	"strings"
	"testing"
	"time"
)

// rfcSecret is RFC 6238's shared test key, ASCII "12345678901234567890",
// in the base32 form the CMS stores.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// TestTOTPCodeRFCVectors pins the algorithm to RFC 6238's Appendix B
// test vectors (SHA-1 rows, truncated to six digits).
func TestTOTPCodeRFCVectors(t *testing.T) {
	vectors := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, v := range vectors {
		got, err := TOTPCode(rfcSecret, time.Unix(v.unix, 0))
		if err != nil {
			t.Fatalf("TOTPCode(t=%d): %v", v.unix, err)
		}
		if got != v.want {
			t.Errorf("TOTPCode(t=%d) = %s, want %s", v.unix, got, v.want)
		}
	}
}

// TestVerifyTOTPSkew: a code from the neighbouring step either side
// passes (clock drift), two steps away does not.
func TestVerifyTOTPSkew(t *testing.T) {
	now := time.Unix(1111111111, 0)
	for _, tc := range []struct {
		name   string
		codeAt time.Time
		want   bool
	}{
		{"current step", now, true},
		{"previous step", now.Add(-30 * time.Second), true},
		{"next step", now.Add(30 * time.Second), true},
		{"two steps back", now.Add(-60 * time.Second), false},
		{"two steps ahead", now.Add(60 * time.Second), false},
	} {
		code, err := TOTPCode(rfcSecret, tc.codeAt)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := VerifyTOTP(rfcSecret, code, now); ok != tc.want {
			t.Errorf("%s: VerifyTOTP = %v, want %v", tc.name, ok, tc.want)
		}
	}
}

// TestVerifyTOTPReturnsMatchedStep: the returned step identifies the code
// that matched, so a caller can refuse to accept that step twice.
func TestVerifyTOTPReturnsMatchedStep(t *testing.T) {
	now := time.Unix(1111111111, 0)
	prev, _ := TOTPCode(rfcSecret, now.Add(-30*time.Second))
	step, ok := VerifyTOTP(rfcSecret, prev, now)
	if !ok {
		t.Fatal("previous-step code rejected")
	}
	if want := now.Add(-30*time.Second).Unix() / 30; step != want {
		t.Errorf("step = %d, want %d", step, want)
	}
}

// TestVerifyTOTPNormalizesInput: people type codes the way apps display
// them — with spaces — and that must not fail the login.
func TestVerifyTOTPNormalizesInput(t *testing.T) {
	now := time.Unix(1111111111, 0)
	if _, ok := VerifyTOTP(rfcSecret, " 050 471 ", now); !ok {
		t.Error("spaced code rejected")
	}
	if _, ok := VerifyTOTP(rfcSecret, "000000", now); ok {
		t.Error("wrong code accepted")
	}
	if _, ok := VerifyTOTP(rfcSecret, "", now); ok {
		t.Error("empty code accepted")
	}
}

func TestGenerateTOTPSecret(t *testing.T) {
	a, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	// 160 bits in unpadded base32 is exactly 32 characters.
	if len(a) != 32 {
		t.Errorf("secret length = %d, want 32", len(a))
	}
	if strings.Contains(a, "=") {
		t.Errorf("secret %q carries padding, which authenticator apps reject", a)
	}
	b, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two generated secrets are identical")
	}
	// The generated secret round-trips through code generation.
	if _, err := TOTPCode(a, time.Unix(59, 0)); err != nil {
		t.Errorf("generated secret does not produce codes: %v", err)
	}
}

// TestTOTPProvisioningURI: the otpauth URL apps enroll from, with the
// label and issuer escaped enough to survive a QR scan.
func TestTOTPProvisioningURI(t *testing.T) {
	uri := TOTPProvisioningURI("example.com", "pat@example.com", rfcSecret)
	for _, want := range []string{
		"otpauth://totp/example.com:pat@example.com?",
		"secret=" + rfcSecret,
		"issuer=example.com",
		"algorithm=SHA1",
		"digits=6",
		"period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("uri missing %q: %s", want, uri)
		}
	}
}

func TestUserTwoFactorEnabled(t *testing.T) {
	u := &User{}
	if u.TwoFactorEnabled() {
		t.Error("empty secret reports enabled")
	}
	u.TOTPSecret = rfcSecret
	if !u.TwoFactorEnabled() {
		t.Error("non-empty secret reports disabled")
	}
}
