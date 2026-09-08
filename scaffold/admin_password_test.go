package scaffold

// The first superadmin's password. A value written into the template
// would be the same on every site ever generated from it, which is a
// published credential rather than a default — so .env gets one generated
// per project, and main.go gets none at all.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var envPasswordRe = regexp.MustCompile(`(?m)^CMS_ADMIN_PASSWORD=(.+)$`)

func generatedEnvPassword(t *testing.T, dir string) string {
	t.Helper()
	m := envPasswordRe.FindStringSubmatch(readFile(t, dir, ".env"))
	if m == nil {
		t.Fatalf("no CMS_ADMIN_PASSWORD in the generated .env:\n%s", readFile(t, dir, ".env"))
	}
	return strings.TrimSpace(m[1])
}

// Two projects generated from the same template must not share a
// password, which is the whole of the fix.
func TestGeneratedAdminPasswordDiffersPerProject(t *testing.T) {
	pw := map[string]bool{}
	for range 8 {
		dir := t.TempDir()
		if _, err := Write(dir, Options{}); err != nil {
			t.Fatal(err)
		}
		p := generatedEnvPassword(t, dir)
		if p == "" {
			t.Fatal("generated an empty admin password")
		}
		if pw[p] {
			t.Fatalf("two projects were generated with the same admin password %q", p)
		}
		pw[p] = true
	}
}

// It has to be worth having: long enough to be unguessable, and made of
// characters somebody can read off a terminal and type into a form.
func TestGeneratedAdminPasswordShape(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	p := generatedEnvPassword(t, dir)

	if got := len(strings.ReplaceAll(p, "-", "")); got < 16 {
		t.Errorf("generated password has %d characters of entropy-bearing text, want at least 16: %q", got, p)
	}
	// The lookalikes are deliberately absent: this is typed by hand at
	// the first login, and "0 or O?" at that moment is a support ticket.
	if i := strings.IndexAny(p, "0O1lI"); i >= 0 {
		t.Errorf("generated password %q contains the ambiguous character %q", p, p[i])
	}
	for _, r := range p {
		if !strings.ContainsRune(adminPasswordAlphabet, r) && r != '-' {
			t.Errorf("generated password %q contains %q, which is outside the alphabet", p, r)
		}
	}
}

// A caller wrapping this package can pin the password — which is also how
// the rest of these tests stay deterministic.
func TestWriteHonoursAnExplicitAdminPassword(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Options{AdminPassword: "correct-horse-battery"}); err != nil {
		t.Fatal(err)
	}
	if got := generatedEnvPassword(t, dir); got != "correct-horse-battery" {
		t.Errorf("CMS_ADMIN_PASSWORD = %q, want the password the caller supplied", got)
	}
}

// The generated program must carry no password of its own. main.go is
// committed; .env is not.
func TestGeneratedMainHasNoFallbackPassword(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Options{AdminPassword: "correct-horse-battery"}); err != nil {
		t.Fatal(err)
	}
	main := readFile(t, dir, "main.go")

	if strings.Contains(main, "correct-horse-battery") {
		t.Error("main.go carries the admin password — it belongs in .env alone")
	}
	if strings.Contains(main, "password123") {
		t.Error("main.go still carries the old shared default password")
	}
	// envOr would supply a fallback; the password must be read in a way
	// that has none.
	if strings.Contains(main, `envOr("CMS_ADMIN_PASSWORD"`) {
		t.Error("main.go reads CMS_ADMIN_PASSWORD through envOr, which means it has a fallback")
	}
	if !strings.Contains(main, `os.Getenv("CMS_ADMIN_PASSWORD")`) {
		t.Error("main.go does not read CMS_ADMIN_PASSWORD without a fallback")
	}
	// And it must not print the credential it was given.
	if strings.Contains(main, `"password", adminPassword`) {
		t.Error("main.go logs the admin password")
	}
}
