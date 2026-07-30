// Package datefmt writes dates in the language they are being read in.
//
// Go's time package formats month and day names in English only, so
// {{.PublishedAt.Format "January 2, 2006"}} on a French page reads
// "July 30, 2026" — the one English string on an otherwise translated
// page. This package holds the small amount of knowledge needed to avoid
// that: how each language the CMS knows orders a date, and what it calls
// the months.
//
// It covers the languages the CMS itself is translated into (English and
// French, matching the admin UI) and falls back to English formatting for
// any other locale, which is the same behaviour a host gets today. A site
// running in a language that isn't here formats its own dates in its own
// templates, as it always could.
package datefmt

import (
	"strconv"
	"strings"
	"time"
)

// frenchMonths is the nominative form used in a date. French does not
// capitalize month names — "30 juillet 2026", not "30 Juillet 2026" —
// and that is not a style choice the caller gets to make by capitalizing
// the format string, the way Go's English layouts allow.
var frenchMonths = [...]string{
	"janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre",
}

var frenchMonthsShort = [...]string{
	"janv.", "févr.", "mars", "avr.", "mai", "juin",
	"juil.", "août", "sept.", "oct.", "nov.", "déc.",
}

// lang reduces a locale to its language: "fr-CA" and "fr" are both
// French. Anything unknown formats as English.
func lang(locale string) string {
	code := strings.ToLower(locale)
	if i := strings.IndexAny(code, "-_"); i >= 0 {
		code = code[:i]
	}
	return code
}

// Long writes a date the way a sentence would: "July 30, 2026" in
// English, "30 juillet 2026" in French. It is what a post's date line
// wants, and what {{cmsDate}} renders.
//
// The first of the month is "1er juillet" in French — the one ordinal
// French uses in a date, and the reason this is not a format string.
func Long(t time.Time, locale string) string {
	switch lang(locale) {
	case "fr":
		return frenchDay(t) + " " + frenchMonths[t.Month()-1] + " " + strconv.Itoa(t.Year())
	default:
		return t.Format("January 2, 2006")
	}
}

// Short writes the same date with the month abbreviated: "Jul 30, 2026",
// "30 juil. 2026". It is for lists and tables, where the full month name
// costs a column its width.
func Short(t time.Time, locale string) string {
	switch lang(locale) {
	case "fr":
		return frenchDay(t) + " " + frenchMonthsShort[t.Month()-1] + " " + strconv.Itoa(t.Year())
	default:
		return t.Format("Jan 2, 2006")
	}
}

func frenchDay(t time.Time) string {
	if t.Day() == 1 {
		return "1er"
	}
	return strconv.Itoa(t.Day())
}
