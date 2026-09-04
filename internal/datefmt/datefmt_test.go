package datefmt

import (
	"testing"
	"time"
)

func TestLongAndShort(t *testing.T) {
	day := time.Date(2026, time.July, 30, 9, 0, 0, 0, time.UTC)
	first := time.Date(2026, time.August, 1, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		t                time.Time
		locale           string
		wantLong, wantSh string
	}{
		{"english", day, "en", "July 30, 2026", "Jul 30, 2026"},
		{"french", day, "fr", "30 juillet 2026", "30 juil. 2026"},
		// French writes the first of the month as an ordinal, and only
		// the first.
		{"french first of month", first, "fr", "1er août 2026", "1er août 2026"},
		{"english first of month", first, "en", "August 1, 2026", "Aug 1, 2026"},
		// Regional codes are the same language.
		{"regional french", day, "fr-CA", "30 juillet 2026", "30 juil. 2026"},
		{"regional english", day, "en_GB", "July 30, 2026", "Jul 30, 2026"},
		// A language the CMS has no date knowledge of reads as English
		// rather than as an error or an empty string.
		{"unknown locale", day, "de", "July 30, 2026", "Jul 30, 2026"},
		{"no locale", day, "", "July 30, 2026", "Jul 30, 2026"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Long(tc.t, tc.locale); got != tc.wantLong {
				t.Errorf("Long = %q, want %q", got, tc.wantLong)
			}
			if got := Short(tc.t, tc.locale); got != tc.wantSh {
				t.Errorf("Short = %q, want %q", got, tc.wantSh)
			}
		})
	}
}

func TestShortTime(t *testing.T) {
	afternoon := time.Date(2026, time.July, 30, 14, 14, 0, 0, time.UTC)
	morning := time.Date(2026, time.August, 1, 9, 5, 0, 0, time.UTC)

	tests := []struct {
		name   string
		t      time.Time
		locale string
		want   string
	}{
		{"english afternoon", afternoon, "en", "Jul 30, 2026, 2:14 PM"},
		// French runs a 24-hour clock and spaces the hour marker.
		{"french afternoon", afternoon, "fr", "30 juil. 2026, 14 h 14"},
		// The zero-padded minute and the ordinal first of the month, in
		// one date.
		{"english morning", morning, "en", "Aug 1, 2026, 9:05 AM"},
		{"french morning", morning, "fr", "1er août 2026, 09 h 05"},
		{"unknown locale", afternoon, "de", "Jul 30, 2026, 2:14 PM"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShortTime(tc.t, tc.locale); got != tc.want {
				t.Errorf("ShortTime = %q, want %q", got, tc.want)
			}
		})
	}
}
