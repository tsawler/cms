package content

import (
	"strings"
	"unicode"

	"github.com/tsawler/cms/internal/dialect"
)

// SearchTerm is one unit of a parsed query; see dialect.SearchTerm.
type SearchTerm = dialect.SearchTerm

// maxSearchTerms bounds how much of a query is honored. A search box
// receives pasted paragraphs, and every extra term is another clause the
// engine has to satisfy; twelve is past what anyone types on purpose.
const maxSearchTerms = 12

// maxSearchQueryLen bounds the raw string before it is even looked at, so
// a megabyte in the query string is discarded rather than tokenized.
const maxSearchQueryLen = 200

// ParseSearchQuery turns what a visitor typed into terms the engines can
// both be asked about.
//
// The grammar is the one every search box has taught people to expect, and
// nothing more: words are AND-ed, "a quoted phrase" must appear intact,
// and a leading - excludes. Everything else a visitor might type — the
// punctuation that means something to one engine's query language and
// something else to the other's, or nothing at all — is dropped here
// rather than passed on. That is what keeps a stray bracket from being a
// syntax error on MySQL and a stray & from being an AND on Postgres.
//
// Characters are kept when they are letters, digits, or marks: the test is
// Unicode's, not ASCII's, so a search in Greek or Japanese survives it.
// Apostrophes and hyphens inside a word are kept too — "l'entreprise" and
// "e-mail" are one word to a reader and should be one term here.
//
// A query with no positive term ("-cat" alone, or nothing but
// punctuation) yields nil: there is nothing to look for, only things to
// avoid, and neither engine agrees with the other about what that means.
func ParseSearchQuery(q string) []SearchTerm {
	if len(q) > maxSearchQueryLen {
		q = q[:maxSearchQueryLen]
	}
	var terms []SearchTerm
	positive := false
	for _, raw := range splitQuery(q) {
		exclude := false
		text := raw.text
		if !raw.phrase {
			// A "-" only excludes at the front of a bare word. Inside one
			// it is part of the word ("e-mail"), and on a phrase it would
			// have been consumed before the quote.
			if t, ok := strings.CutPrefix(text, "-"); ok {
				exclude, text = true, t
			}
		} else if t, ok := strings.CutPrefix(text, "-"); ok {
			exclude, text = true, t
		}
		text = cleanTerm(text, raw.phrase)
		if text == "" {
			continue
		}
		if !exclude {
			positive = true
		}
		terms = append(terms, SearchTerm{Text: text, Phrase: raw.phrase, Exclude: exclude})
		if len(terms) == maxSearchTerms {
			break
		}
	}
	if !positive {
		return nil
	}
	return terms
}

// rawTerm is one token as it came out of the string, before its characters
// are vetted.
type rawTerm struct {
	text   string
	phrase bool
}

// splitQuery breaks the string on whitespace, except inside double quotes.
// An unclosed quote runs to the end rather than being an error — someone
// typing a phrase has not finished typing it, and refusing the query would
// be a worse answer than searching for the words they got out.
func splitQuery(q string) []rawTerm {
	var out []rawTerm
	var cur strings.Builder
	inQuote := false
	// Whether the token being built started with a quote, so that the "-"
	// in -"a phrase" is still visible to the caller.
	prefix := ""
	flush := func(phrase bool) {
		if cur.Len() > 0 {
			out = append(out, rawTerm{text: prefix + cur.String(), phrase: phrase})
		}
		cur.Reset()
		prefix = ""
	}
	for _, r := range q {
		switch {
		case r == '"':
			if inQuote {
				flush(true)
				inQuote = false
				continue
			}
			// Opening quote: whatever was being built is its own word,
			// and a leading "-" carries onto the phrase.
			if cur.String() == "-" {
				cur.Reset()
				prefix = "-"
			} else {
				flush(false)
			}
			inQuote = true
		case unicode.IsSpace(r) && !inQuote:
			flush(false)
		default:
			cur.WriteRune(r)
		}
	}
	flush(inQuote)
	return out
}

// cleanTerm drops the characters neither engine should be shown. Inside a
// phrase, spaces survive — that is what makes it a phrase — and a run of
// dropped characters collapses to one space so the words either side stay
// separate.
func cleanTerm(s string, phrase bool) string {
	var b strings.Builder
	lastDropped := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsMark(r):
			b.WriteRune(r)
			lastDropped = false
		case r == '\'' || r == '’' || r == '-':
			// Kept only between two kept characters, so a term never
			// begins or ends with one — a trailing hyphen would be an
			// operator to MySQL and a dangling word to Postgres.
			if b.Len() > 0 && !lastDropped {
				b.WriteRune(r)
			}
		case phrase && unicode.IsSpace(r):
			if b.Len() > 0 {
				b.WriteRune(' ')
			}
			lastDropped = true
		default:
			if phrase && b.Len() > 0 {
				b.WriteRune(' ')
			}
			lastDropped = true
		}
	}
	return strings.TrimRight(strings.TrimSpace(b.String()), "'’-")
}
