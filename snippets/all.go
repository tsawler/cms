package snippets

// This file exists so that a host application can name the module's
// whole block library once and never revisit the decision.
//
// The problem it solves is a quiet one. The library arrived in pieces —
// the Tailwind-first defaults first, the imported ContentBuilder pack
// later, section presets alongside both — and each piece got its own
// exported constructor. A host composing a palette therefore had to
// enumerate them, and every such enumeration is a snapshot of what the
// module shipped on the day it was written. Add a block to a list an
// application already names and it arrives on the next upgrade; add a
// *list* and it never arrives at all, in any application, and nothing
// fails to tell anybody. Sites drift apart from the module and from each
// other, one forgotten constructor at a time.
//
// All() is the fix, and the test beside it is what makes it one: a new
// exported []Snippet constructor that All does not fold in fails the
// build. So "everything the module ships" stays a true statement rather
// than a comment somebody has to remember to update.

// All returns every block this module ships: the Tailwind-first defaults
// and the imported library, inline snippets and section presets
// together. It is what Config.Snippets uses when a host leaves it nil,
// and what a host should start from when it doesn't.
//
// Nothing here is stored in the database. Config snippets are registered
// in code on every startup and merged with admin-created ones when the
// palette is served, so a release that adds blocks delivers them on the
// next `go get -u` and rebuild — there is no seed to re-run and no
// migration to write, and an editor's own snippets are untouched.
//
// Composing on top of it keeps that property, because the customization
// is expressed against whatever the module ships rather than against a
// list copied out of it:
//
//	cfg.Snippets = append(snippets.All(), mySnippets()...)         // add
//	cfg.Snippets = slices.DeleteFunc(snippets.All(),               // drop
//		func(s cms.Snippet) bool { return s.Group == "Buttons" })
//
// Order is the order the palette shows: inline blocks before section
// presets, the module's own designs before the imported ones. A host's
// additions go where the host puts them.
func All() []Snippet {
	def, lib := DefaultSnippets(), LibrarySnippets()
	defSec, libSec := DefaultSectionPresets(), LibrarySectionPresets()
	out := make([]Snippet, 0, len(def)+len(lib)+len(defSec)+len(libSec))
	out = append(out, def...)
	out = append(out, lib...)
	out = append(out, defSec...)
	out = append(out, libSec...)
	return out
}
