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
	return versionOf(debug.ReadBuildInfo())
}

// versionOf is Version for a given reading of the build info.
func versionOf(info *debug.BuildInfo, ok bool) string {
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
			// A replace by a module version has one; a replace by a
			// directory — a local checkout — has none, and that is
			// what "(devel)" says.
			if dep.Replace.Version == "" {
				return "(devel)"
			}
			return dep.Replace.Version
		}
		return dep.Version
	}
	return "unknown"
}
