// The dashboard: host section cards first, the built-in cards for
// superadmins, and a seven-day traffic chart under them.
//
// The chart is inline SVG with geometry computed here rather than styled
// in the browser, because the admin's CSP allows no inline styles: every
// coordinate is an SVG attribute and every colour comes from a stylesheet
// class. One bar per day, a count label on the busiest day only, and a
// hover tooltip per column: each column carries its readout in data
// attributes, and admin.js shows a styled tip instantly — the native
// <title> tooltip appears on the browser's own slow schedule and takes
// no styling.

package admin

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/datefmt"
)

func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	td := s.newTemplateData(r)
	td.DashCards = s.dashCards(r.Context(), td.User)
	td.Traffic = s.trafficChart(r)
	s.render(w, http.StatusOK, "dashboard", td)
}

// dashCard is one host section's card on the dashboard, ready to render.
type dashCard struct {
	URL, Title, Description string
	Note                    string
	Count                   int
	HasCount                bool
}

// dashCards builds the cards for the host sections the user may see, in
// registration order. Counts follow the NavCount contract: an error is
// logged and renders as zero.
func (s *server) dashCards(ctx context.Context, u *auth.User) []dashCard {
	var cards []dashCard
	for _, sec := range s.deps.Sections {
		if sec.Dashboard == nil || !sectionVisibleTo(sec, u) {
			continue
		}
		card := dashCard{
			URL:         s.deps.AdminPath + SectionPathPrefix + "/" + sec.Path + "/",
			Title:       sec.Dashboard.Title,
			Description: sec.Dashboard.Description,
		}
		if card.Title == "" {
			card.Title = sec.NavLabel
		}
		count := sec.Dashboard.Count
		if count == nil {
			count = sec.NavCount
		}
		if count != nil {
			card.HasCount = true
			n, err := count(ctx)
			if err != nil {
				s.deps.Logger.Error("cms admin: counting dashboard card items", "section", card.Title, "err", err)
			} else {
				card.Count = n
			}
		}
		if sec.Dashboard.Note != nil {
			note, err := sec.Dashboard.Note(ctx)
			if err != nil {
				s.deps.Logger.Error("cms admin: building dashboard card note", "section", card.Title, "err", err)
			} else {
				card.Note = note
			}
		}
		cards = append(cards, card)
	}
	return cards
}

// trafficDays is how many daily bars the dashboard chart shows.
const trafficDays = 7

// topPagesShown is how many paths the "Top pages" list beside the
// chart holds.
const topPagesShown = 5

// Chart geometry, in viewBox units. The left gutter holds the axis
// labels; the top leaves room for the count label above the tallest bar.
const (
	chartW        = 640.0
	chartH        = 190.0
	chartLeft     = 40.0 // plot's left edge; tick labels end at chartLeft-8
	chartRight    = 632.0
	chartTop      = 18.0
	chartBaseline = 160.0 // y of the zero line
	chartLabelY   = 182.0 // y of the weekday labels
)

// trafficChart is the dashboard's seven-day page-view chart, as template
// data: pre-computed SVG geometry plus the numbers behind it.
type trafficChart struct {
	ViewW, ViewH  float64
	PlotX, PlotX2 float64 // baseline and gridline extent
	TickX         float64 // right edge of the axis labels
	Baseline      float64
	Bars          []trafficBar
	Ticks         []trafficTick
	TopPages      []content.PathViews // the week's busiest paths, beside the chart
	Total         int
	HasViews      bool // false renders the "no traffic yet" note instead
}

// trafficBar is one day's column: the bar outline (empty on a zero day),
// an invisible full-column hover target, the weekday label under it, and
// the tooltip text for the whole column — TipViews and TipDate feed the
// styled hover tip, Title is the same readout in one line for the
// column's aria-label.
type trafficBar struct {
	Path           string
	HitX, HitY     float64
	HitW, HitH     float64
	LabelX, LabelY float64
	Label          string
	Title          string
	TipViews       string
	TipDate        string
	Count          string // the busiest day's label, digits grouped
	CountX, CountY float64
	ShowCount      bool
}

// trafficTick is one horizontal gridline with its value label.
type trafficTick struct {
	Y     float64
	Label int
}

// trafficChart loads the last seven days of public page views and lays
// the chart out. Nil — no chart section at all — when the store is
// absent (tests, partial Deps) or the query fails; a site with no
// recorded views yet gets a chart frame with an honest note instead.
func (s *server) trafficChart(r *http.Request) *trafficChart {
	if s.deps.Content == nil {
		return nil
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	from := today.AddDate(0, 0, -(trafficDays - 1))
	byDay, err := s.deps.Content.PageViewsByDay(r.Context(), from, today)
	if err != nil {
		s.deps.Logger.Error("cms admin: loading page views", "err", err)
		return nil
	}

	counts := make([]int, trafficDays)
	maxViews, total := 0, 0
	for i := range counts {
		counts[i] = byDay[from.AddDate(0, 0, i).Format("2006-01-02")]
		total += counts[i]
		maxViews = max(maxViews, counts[i])
	}

	c := &trafficChart{
		ViewW: chartW, ViewH: chartH,
		PlotX: chartLeft, PlotX2: chartRight,
		TickX:    chartLeft - 8,
		Baseline: chartBaseline,
		Total:    total,
		HasViews: total > 0,
	}
	if c.HasViews {
		c.TopPages, err = s.deps.Content.TopPages(r.Context(), from, today, topPagesShown)
		if err != nil {
			// The chart stands on its own; the list is a bonus.
			s.deps.Logger.Error("cms admin: loading top pages", "err", err)
		}
	}

	top := niceCeil(maxViews)
	scale := (chartBaseline - chartTop) / float64(top)
	c.Ticks = []trafficTick{
		{Y: round1(chartBaseline - float64(top)*scale), Label: top},
		{Y: round1(chartBaseline - float64(top/2)*scale), Label: top / 2},
	}

	lang := s.adminLang(r)
	slot := (chartRight - chartLeft) / trafficDays
	barW := math.Round(slot * 0.52)
	labeled := false // count label goes on the first busiest day only
	for i, n := range counts {
		day := from.AddDate(0, 0, i)
		x0 := chartLeft + float64(i)*slot
		bx := round1(x0 + (slot-barW)/2)
		h := float64(n) * scale
		b := trafficBar{
			HitX: round1(x0), HitY: chartTop,
			HitW: round1(slot), HitH: chartLabelY - chartTop,
			LabelX: round1(x0 + slot/2), LabelY: chartLabelY,
			Label:    s.tr(r, day.Weekday().String()[:3]),
			TipViews: groupDigits(n, lang) + " " + s.tr(r, "page views"),
			TipDate:  datefmt.Short(day, lang),
			Count:    groupDigits(n, lang),
		}
		b.Title = b.TipDate + " — " + b.TipViews
		if n > 0 {
			b.Path = barPath(bx, chartBaseline, barW, h)
			if n == maxViews && !labeled {
				labeled = true
				b.ShowCount = true
				b.CountX = round1(x0 + slot/2)
				b.CountY = round1(chartBaseline - h - 6)
			}
		}
		c.Bars = append(c.Bars, b)
	}
	return c
}

// barPath draws a bar of width w rising h from the baseline at y, with
// the top two corners rounded — data ends are soft, the baseline end is
// square. The radius yields to very short or narrow bars.
func barPath(x, baseline, w, h float64) string {
	r := math.Min(4, math.Min(h, w/2))
	top := baseline - h
	return fmt.Sprintf("M%g %gV%gQ%g %g %g %gH%gQ%g %g %g %gV%gZ",
		round1(x), round1(baseline),
		round1(top+r),
		round1(x), round1(top), round1(x+r), round1(top),
		round1(x+w-r),
		round1(x+w), round1(top), round1(x+w), round1(top+r),
		round1(baseline))
}

// niceCeil rounds a maximum up to a chart-friendly axis top: the next
// value in 2, 4, 6, 8, 10, 20, 40, ... — a ladder whose every rung
// halves to a whole number, so the midline label is never a fraction.
// Zero data still gets a scale (2), so an empty chart has honest axes.
func niceCeil(n int) int {
	for m := 1; ; m *= 10 {
		for _, f := range []int{2, 4, 6, 8, 10} {
			if v := f * m; v >= n {
				return v
			}
		}
	}
}

// round1 keeps SVG coordinates to one decimal, so templates emit "84.6"
// rather than a float64's full expansion.
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// groupDigits writes a count the way the admin language reads one:
// "1,234" in English, "1 234" — a non-breaking space — in French. The
// chart's counts are never negative.
func groupDigits(n int, lang string) string {
	sep := ","
	if lang == "fr" {
		sep = "\u00a0"
	}
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(d)
	}
	return b.String()
}
