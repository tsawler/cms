package render

import (
	"regexp"
	"strings"
)

var classAttrRe = regexp.MustCompile(`class\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// ClassTokens returns the tokens of every class attribute in an HTML
// fragment, as written — entities and all — in document order, with
// repeats. It reads markup the way the Tailwind build does: a class token
// is whatever whitespace separates, wherever a class attribute appears.
func ClassTokens(html string) []string {
	var out []string
	for _, m := range classAttrRe.FindAllStringSubmatch(html, -1) {
		list := m[1]
		if list == "" {
			list = m[2]
		}
		out = append(out, strings.Fields(list)...)
	}
	return out
}

// IsArbitraryClass reports whether a class token uses Tailwind's
// arbitrary-value syntax — bg-[url(...)], [mask:none], [&_p]:hidden,
// bg-(--brand) — rather than naming a utility from the configured scale.
// Such a token compiles to whatever CSS it spells out, including
// selectors reaching outside the element it sits on and URLs fetched by
// every visitor.
func IsArbitraryClass(tok string) bool {
	return strings.ContainsAny(tok, "[]()")
}
