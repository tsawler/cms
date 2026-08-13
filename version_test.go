package cms

import "testing"

// TestVersion pins down the fallback behaviour: whatever build context the
// test binary has, Version must return something printable, never "".
func TestVersion(t *testing.T) {
	if v := Version(); v == "" {
		t.Fatal("Version() returned an empty string")
	}
}
