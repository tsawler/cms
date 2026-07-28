package dialect

import (
	"strconv"
	"strings"
)

// The rewriter walks a statement once, copying string literals, quoted
// identifiers, and comments through untouched and translating only the SQL
// around them. Everything it does is driven by that scan, so a `$1` or the
// word EXCLUDED inside a string literal is left alone.

// rewrite translates canonical Postgres SQL into the MySQL/MariaDB form and
// reorders args to match.
//
// Three translations happen:
//
//	$N                                      -> ?
//	ON CONFLICT (...) DO UPDATE SET         -> ON DUPLICATE KEY UPDATE
//	EXCLUDED.col                            -> VALUES(col)
//
// VALUES(col) is used rather than MySQL 8.0.20's `AS new` row alias because
// MariaDB does not support the alias form; VALUES() works on both.
func rewrite(query string, args []any) (string, []any) {
	var out strings.Builder
	out.Grow(len(query))
	// Placeholders are emitted in the order they appear in the text, which
	// need not be numeric order, and a number may repeat. Rebuilding the
	// slice per occurrence handles both: `... $2 ... $1 ... $1` becomes
	// `? ? ?` with args [a2, a1, a1].
	newArgs := make([]any, 0, len(args))
	// A statement already written with native ? placeholders has no $N to
	// rebuild from, and must keep the arguments it came with rather than
	// losing them to an empty slice.
	sawPlaceholder := false

	for i := 0; i < len(query); {
		switch c := query[i]; {
		case c == '\'', c == '"', c == '`':
			j := scanQuoted(query, i)
			out.WriteString(query[i:j])
			i = j

		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			j := scanLineComment(query, i)
			out.WriteString(query[i:j])
			i = j

		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			j := scanBlockComment(query, i)
			out.WriteString(query[i:j])
			i = j

		case c == '$' && i+1 < len(query) && isDigit(query[i+1]):
			j := i + 1
			for j < len(query) && isDigit(query[j]) {
				j++
			}
			n, _ := strconv.Atoi(query[i+1 : j])
			if n < 1 || n > len(args) {
				// A placeholder with no argument is a bug in the CMS's own
				// SQL. Pass it through so the driver reports a syntax error
				// naming the offending $N rather than silently binding NULL.
				out.WriteString(query[i:j])
			} else {
				out.WriteByte('?')
				newArgs = append(newArgs, args[n-1])
				sawPlaceholder = true
			}
			i = j

		default:
			if j, ok := matchOnConflict(query, i); ok {
				out.WriteString("ON DUPLICATE KEY UPDATE")
				i = j
				continue
			}
			if col, j, ok := matchExcluded(query, i); ok {
				out.WriteString("VALUES(")
				out.WriteString(col)
				out.WriteByte(')')
				i = j
				continue
			}
			out.WriteByte(c)
			i++
		}
	}
	if !sawPlaceholder {
		return out.String(), args
	}
	return out.String(), newArgs
}

// splitStatements breaks a script on top-level semicolons, ignoring those
// inside literals and comments, and drops the blank remainder.
func splitStatements(script string) []string {
	var out []string
	start := 0
	for i := 0; i < len(script); {
		switch c := script[i]; {
		case c == '\'', c == '"', c == '`':
			i = scanQuoted(script, i)
		case c == '-' && i+1 < len(script) && script[i+1] == '-':
			i = scanLineComment(script, i)
		case c == '/' && i+1 < len(script) && script[i+1] == '*':
			i = scanBlockComment(script, i)
		case c == ';':
			if stmt := strings.TrimSpace(script[start:i]); stmt != "" {
				out = append(out, stmt)
			}
			i++
			start = i
		default:
			i++
		}
	}
	if stmt := strings.TrimSpace(script[start:]); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

// scanQuoted returns the index just past the quoted run starting at i. A
// doubled quote (” or “) escapes itself and a backslash escapes the next
// byte, matching both engines' string rules.
func scanQuoted(s string, i int) int {
	quote := s[i]
	i++
	for i < len(s) {
		switch s[i] {
		case '\\':
			i += 2
		case quote:
			if i+1 < len(s) && s[i+1] == quote {
				i += 2 // doubled: an escaped quote, not the end
				continue
			}
			return i + 1
		default:
			i++
		}
	}
	return len(s) // unterminated; the driver will complain
}

// scanLineComment returns the index just past a -- comment, including its
// newline.
func scanLineComment(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j + 1
	}
	return len(s)
}

// scanBlockComment returns the index just past a /* */ comment.
func scanBlockComment(s string, i int) int {
	if j := strings.Index(s[i+2:], "*/"); j >= 0 {
		return i + 2 + j + 2
	}
	return len(s)
}

// matchOnConflict recognizes `ON CONFLICT (...) DO UPDATE SET` starting at i
// and returns the index just past it.
//
// Only that exact shape is translated. `ON CONFLICT ... DO NOTHING` has no
// single-statement MySQL equivalent (INSERT IGNORE changes the verb), so it
// is deliberately left untranslated: the engine then reports a syntax error
// instead of the statement silently doing something else.
func matchOnConflict(s string, i int) (int, bool) {
	if !isWordStart(s, i) {
		return 0, false
	}
	j, ok := matchWords(s, i, "ON", "CONFLICT")
	if !ok {
		return 0, false
	}
	j = skipSpace(s, j)
	// The conflict target, if present: MySQL infers it from the unique keys.
	if j < len(s) && s[j] == '(' {
		depth := 0
		for j < len(s) {
			switch s[j] {
			case '(':
				depth++
			case ')':
				depth--
			case '\'', '"', '`':
				j = scanQuoted(s, j)
				continue
			}
			j++
			if depth == 0 {
				break
			}
		}
		j = skipSpace(s, j)
	}
	j, ok = matchWords(s, j, "DO", "UPDATE", "SET")
	if !ok {
		return 0, false
	}
	return j, true
}

// matchExcluded recognizes `EXCLUDED.col` starting at i, returning the column
// name and the index just past it.
func matchExcluded(s string, i int) (string, int, bool) {
	if !isWordStart(s, i) {
		return "", 0, false
	}
	j, ok := matchWords(s, i, "EXCLUDED")
	if !ok || j >= len(s) || s[j] != '.' {
		return "", 0, false
	}
	j++
	start := j
	if j < len(s) && s[j] == '`' {
		j = scanQuoted(s, j)
		return s[start:j], j, true
	}
	for j < len(s) && isIdent(s[j]) {
		j++
	}
	if j == start {
		return "", 0, false
	}
	return s[start:j], j, true
}

// matchWords matches a sequence of keywords case-insensitively, allowing any
// run of whitespace between them, and returns the index just past the last.
func matchWords(s string, i int, words ...string) (int, bool) {
	for n, w := range words {
		if n > 0 {
			j := skipSpace(s, i)
			if j == i {
				return 0, false // keywords must be separated
			}
			i = j
		}
		if !hasWordFold(s, i, w) {
			return 0, false
		}
		i += len(w)
	}
	return i, true
}

// hasWordFold reports whether w appears at i, case-insensitively, and is not
// glued to a longer identifier.
func hasWordFold(s string, i int, w string) bool {
	if i+len(w) > len(s) || !strings.EqualFold(s[i:i+len(w)], w) {
		return false
	}
	end := i + len(w)
	return end == len(s) || !isIdent(s[end])
}

// isWordStart reports whether i begins an identifier rather than continuing
// one, so EXCLUDED does not match inside my_excluded.
func isWordStart(s string, i int) bool {
	if !isIdent(s[i]) {
		return false
	}
	return i == 0 || !isIdent(s[i-1])
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || isDigit(c)
}
