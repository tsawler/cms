package content

import (
	"html"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// searchTextPolicy strips a block's HTML down to the words a visitor
// actually reads.
//
// bluemonday's strict policy is exactly the right tool and is already a
// dependency: it allows no elements at all, and — the part a naive tag
// stripper gets wrong — it elides the *contents* of the elements whose
// text nobody sees, script and style among them. A regexp that deleted
// everything between < and > would leave a page's JavaScript sitting in
// the index as prose.
//
// It is built once: policies are read-only after construction and safe to
// share across goroutines.
var searchTextPolicy = bluemonday.StrictPolicy()

// SearchText renders a block's stored HTML as the plain text the search
// index holds: no markup, no entities, no runs of whitespace.
//
// Three things happen in an order that matters.
//
// Every "<" is padded with a space first. bluemonday removes tags without
// putting anything in their place, so "<p>one</p><p>two</p>" would come
// out as "onetwo" — one word that appears on no page, and two words that
// can no longer be found. Padding is cruder than walking the tree and
// asking which elements are block-level, and it is also right in every
// case: an extra space between two words is collapsed away below, and a
// missing one is a wrong index. It is safe because it only ever inserts
// whitespace ahead of a "<" that bluemonday is about to consume anyway.
//
// Then the sanitize, which drops the markup and the invisible elements'
// contents.
//
// Then the entities are decoded, because bluemonday escapes what it emits
// — it is built to produce HTML, and this is the one caller that wants
// text. Without it the index would hold "&amp;" where the page says "&",
// and a search for "R&D" would find nothing.
func SearchText(htmlText string) string {
	if htmlText == "" {
		return ""
	}
	padded := strings.ReplaceAll(htmlText, "<", " <")
	return collapseSpace(html.UnescapeString(searchTextPolicy.Sanitize(padded)))
}

// collapseSpace reduces every run of whitespace to a single space and
// trims the ends. Newlines and tabs from the source markup's own
// formatting are whitespace like any other here: the index stores words,
// not layout.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// DefaultSnippetWords is how many words of context a result shows around
// the words that matched. Enough for a sentence and a bit, which is what a
// reader needs to tell whether this is the page they meant.
const DefaultSnippetWords = 30

// Snippet cuts the part of an indexed body worth showing under a search
// result: a window of words around the first term that matched, with an
// ellipsis on whichever side was cut.
//
// It works on words rather than characters because the body is already
// space-normalized (SearchText leaves exactly one space between words), so
// a window of words is a window of whole words, and a snippet never ends
// mid-syllable.
//
// Matching is deliberately looser here than in the index: a substring
// test, case-folded, with no stemming. The index decides *which* pages
// come back; this only decides where to point in one of them, and a
// stemmed match ("returning" for "return") should still be pointed at
// rather than sending the reader to the top of the page.
//
// With no term found — the match was on the title, or on a stemmed form no
// substring test finds — the opening words are returned, which is the same
// thing a page's own summary would have said.
func Snippet(body string, terms []SearchTerm, words int) string {
	if words <= 0 {
		words = DefaultSnippetWords
	}
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return ""
	}
	at := firstMatch(fields, terms)
	if at < 0 {
		return joinSnippet(fields, 0, words, len(fields) > words)
	}
	// Centred on the hit, then pushed back inside the text at either end,
	// so a match in the first line still shows a full window.
	start := at - words/2
	if start+words > len(fields) {
		start = len(fields) - words
	}
	start = max(start, 0)
	return joinSnippet(fields, start, words, start+words < len(fields))
}

// firstMatch is the index of the earliest word holding any of the query's
// positive terms, or -1. A phrase is looked for by its first word: the
// window is wide enough that the rest of it comes along.
//
// The words are the outer loop so that each one is case-folded once
// rather than once per term — a page of a few thousand words searched for
// a handful of them is the ordinary case, and the other nesting does that
// work over again for every term.
func firstMatch(fields []string, terms []SearchTerm) int {
	needles := make([]string, 0, len(terms))
	for _, t := range terms {
		if t.Exclude {
			continue
		}
		n := strings.ToLower(t.Text)
		if i := strings.IndexByte(n, ' '); i > 0 {
			n = n[:i]
		}
		if n != "" {
			needles = append(needles, n)
		}
	}
	if len(needles) == 0 {
		return -1
	}
	for i, w := range fields {
		lower := strings.ToLower(w)
		for _, n := range needles {
			if strings.Contains(lower, n) {
				return i
			}
		}
	}
	return -1
}

// joinSnippet renders the window, marking each end that was cut.
func joinSnippet(fields []string, start, words int, cutEnd bool) string {
	end := min(start+words, len(fields))
	out := strings.Join(fields[start:end], " ")
	if start > 0 {
		out = "… " + out
	}
	if cutEnd {
		out += " …"
	}
	return out
}
