package content

// Public-site traffic, kept as one counter per day and path. Recording is
// a single upsert on the page-serving path and reading is a single
// aggregate on the admin dashboard, so the table needs no more shape than
// that: no sessions, no visitors, no user agents — page impressions only.

import (
	"context"
	"slices"
	"strings"
	"time"
)

// viewPathMax matches the cms_page_views.path column. Paths longer than
// this are truncated rather than dropped: a byte-perfect URL matters less
// than the day's count staying right.
const viewPathMax = 512

// dayFormat is how days are keyed: dates, in UTC, no clock.
const dayFormat = "2006-01-02"

// RecordPageView adds one to the counter for path on the given day
// (taken as a UTC date). Concurrent instances land on the same row; the
// upsert makes the increment atomic.
func (s *Store) RecordPageView(ctx context.Context, day time.Time, path string) error {
	if len(path) > viewPathMax {
		path = path[:viewPathMax]
	}
	// The increment reads the existing row's counter, qualified with the
	// table name: Postgres calls a bare "views" ambiguous here, and the
	// qualified form means the same thing on MySQL and MariaDB.
	_, err := s.db.Exec(ctx, `
		INSERT INTO cms_page_views (day, path, views) VALUES ($1, $2, 1)
		ON CONFLICT (day, path) DO UPDATE SET views = cms_page_views.views + 1`,
		day.UTC().Format(dayFormat), path)
	return err
}

// PageViewsByDay sums the recorded views per day over [from, to], both
// taken as UTC dates. The result maps "2006-01-02" keys to totals; days
// with no traffic are simply absent, so callers fill their own zeroes.
// The summing happens here rather than in SQL because SUM's result type
// is engine-flavoured (numeric, DECIMAL) while the per-row counters scan
// as plain integers everywhere — and a week holds few rows.
func (s *Store) PageViewsByDay(ctx context.Context, from, to time.Time) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT day, views FROM cms_page_views
		WHERE day BETWEEN $1 AND $2`,
		from.UTC().Format(dayFormat), to.UTC().Format(dayFormat))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var day time.Time
		var views int
		if err := rows.Scan(&day, &views); err != nil {
			return nil, err
		}
		out[day.Format(dayFormat)] += views
	}
	return out, rows.Err()
}

// PathViews is one path's view total over a queried range.
type PathViews struct {
	Path  string
	Views int
}

// TopPages returns the most-viewed paths over [from, to] (UTC dates,
// inclusive), busiest first, at most limit of them. Ties break on path
// so the order is stable across renders. Summed in Go for the same
// reason PageViewsByDay is: the per-row counters scan as plain integers
// on every engine, and a season of a real site's counters is small.
func (s *Store) TopPages(ctx context.Context, from, to time.Time, limit int) ([]PathViews, error) {
	rows, err := s.db.Query(ctx, `
		SELECT path, views FROM cms_page_views
		WHERE day BETWEEN $1 AND $2`,
		from.UTC().Format(dayFormat), to.UTC().Format(dayFormat))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	totals := make(map[string]int)
	for rows.Next() {
		var path string
		var views int
		if err := rows.Scan(&path, &views); err != nil {
			return nil, err
		}
		totals[path] += views
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]PathViews, 0, len(totals))
	for path, views := range totals {
		out = append(out, PathViews{Path: path, Views: views})
	}
	slices.SortFunc(out, func(a, b PathViews) int {
		if a.Views != b.Views {
			return b.Views - a.Views
		}
		return strings.Compare(a.Path, b.Path)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// PrunePageViews deletes counters for days before the given UTC date.
// The dashboard charts a week; keeping a season of history costs almost
// nothing and leaves room for a longer chart later, but the table should
// not grow forever, so Migrate calls this on every startup.
func (s *Store) PrunePageViews(ctx context.Context, before time.Time) error {
	_, err := s.db.Exec(ctx, `DELETE FROM cms_page_views WHERE day < $1`,
		before.UTC().Format(dayFormat))
	return err
}
