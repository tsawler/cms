// Module version reporting. The Go toolchain stamps every binary with the
// resolved version of each module it was built from; Version surfaces the
// cms module's entry so hosts can show which CMS release they are running.

package cms

import "runtime/debug"

// modulePath is the module directive in go.mod, which is how the cms
// module is named in a host binary's build info.
const modulePath = "github.com/tsawler/cms"

// Version reports the version of the cms module compiled into the running
// binary, as recorded by the Go toolchain at build time:
//
//   - a release tag like "v0.9.4" when the host was built against a
//     published version resolved through go.mod;
//   - "(devel)" when the module came from a local checkout instead, e.g.
//     through a go.work workspace or a replace directive, so no tag
//     describes the code;
//   - "unknown" when the binary carries no build info at all (test
//     binaries and some non-module builds).
//
// The value comes from debug.ReadBuildInfo, so it is always what the
// binary was actually built from — there is no constant to bump when
// tagging a release.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Path == modulePath {
		return info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path != modulePath {
			continue
		}
		if dep.Replace != nil {
			return dep.Replace.Version
		}
		return dep.Version
	}
	return "unknown"
}
