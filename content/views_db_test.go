package content_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

func TestPageViews(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		day := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
		prev := day.AddDate(0, 0, -1)
		old := day.AddDate(0, 0, -100)
		record := func(d time.Time, path string) {
			t.Helper()
			if err := s.RecordPageView(ctx, d, path); err != nil {
				t.Fatalf("recording %s %s: %v", d.Format("2006-01-02"), path, err)
			}
		}

		// Same day and path lands on one counter; other paths and days
		// get their own.
		record(day, "/")
		record(day, "/")
		record(day, "/inventory")
		record(prev, "/")
		record(old, "/")

		got, err := s.PageViewsByDay(ctx, day.AddDate(0, 0, -6), day)
		if err != nil {
			t.Fatalf("PageViewsByDay: %v", err)
		}
		want := map[string]int{"2026-08-10": 3, "2026-08-09": 1}
		if len(got) != len(want) {
			t.Fatalf("PageViewsByDay = %v, want %v", got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("views[%s] = %d, want %d", k, got[k], v)
			}
		}

		// A path longer than the column is truncated, not dropped: the
		// day's count still moves.
		record(day, "/"+strings.Repeat("x", 600))
		if got, err = s.PageViewsByDay(ctx, day, day); err != nil || got["2026-08-10"] != 4 {
			t.Errorf("views after long path = %v (err %v), want 4 on 2026-08-10", got, err)
		}

		// Top pages: summed across the range's days, busiest first, ties
		// broken on path, capped at the limit. The truncated-path row
		// and the old day sit outside the queried range or below the
		// leaders, exercising both the range filter and the cap.
		record(prev, "/inventory")
		record(prev, "/inventory")
		record(day, "/")
		top, err := s.TopPages(ctx, day.AddDate(0, 0, -6), day, 2)
		if err != nil {
			t.Fatalf("TopPages: %v", err)
		}
		if len(top) != 2 ||
			top[0] != (content.PathViews{Path: "/", Views: 4}) ||
			top[1] != (content.PathViews{Path: "/inventory", Views: 3}) {
			t.Errorf("TopPages = %+v, want / (4) then /inventory (3)", top)
		}

		// Pruning removes only days before the cutoff.
		if err := s.PrunePageViews(ctx, day.AddDate(0, 0, -90)); err != nil {
			t.Fatalf("PrunePageViews: %v", err)
		}
		all, err := s.PageViewsByDay(ctx, old, day)
		if err != nil {
			t.Fatalf("PageViewsByDay after prune: %v", err)
		}
		if all[old.Format("2006-01-02")] != 0 {
			t.Error("pruning left the old day's counter behind")
		}
		if all["2026-08-10"] != 5 || all["2026-08-09"] != 3 {
			t.Errorf("pruning touched days inside the retention window: %v", all)
		}
	})
}
