package cms

import (
	"runtime/debug"
	"testing"
)

// TestVersion pins down the fallback behaviour: whatever build context the
// test binary has, Version must return something printable, never "".
func TestVersion(t *testing.T) {
	if v := Version(); v == "" {
		t.Fatal("Version() returned an empty string")
	}
}

// Every way the cms module can be compiled into a binary, and what each
// reports. A replace by a directory carries no version of its own, and
// used to come back as "" — a blank in the admin's footer.
func TestVersionOf(t *testing.T) {
	dep := func(version string, replace *debug.Module) *debug.BuildInfo {
		return &debug.BuildInfo{
			Main: debug.Module{Path: "example.com/host"},
			Deps: []*debug.Module{{Path: modulePath, Version: version, Replace: replace}},
		}
	}
	cases := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info", nil, false, "unknown"},
		{"the cms module itself", &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}}, true, "(devel)"},
		{"a released dependency", dep("v1.2.3", nil), true, "v1.2.3"},
		{"replaced by another version", dep("v1.2.3", &debug.Module{Path: "example.com/fork", Version: "v1.2.4"}), true, "v1.2.4"},
		{"replaced by a directory", dep("v1.2.3", &debug.Module{Path: "../cms"}), true, "(devel)"},
		{"not a dependency at all", &debug.BuildInfo{Main: debug.Module{Path: "example.com/host"}}, true, "unknown"},
	}
	for _, c := range cases {
		if got := versionOf(c.info, c.ok); got != c.want {
			t.Errorf("%s: versionOf = %q, want %q", c.name, got, c.want)
		}
	}
}
