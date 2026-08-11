package admin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
)

func dashTestServer(sections []Section) *server {
	return &server{deps: Deps{
		AdminPath: "/admin",
		Sections:  sections,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

func TestDashCards(t *testing.T) {
	sections := []Section{
		{Path: "inventory", NavLabel: "Inventory", Permission: "vehicles",
			NavCount: func(context.Context) (int, error) { return 42, nil },
			Dashboard: &DashboardCard{Title: "Vehicles", Description: "Pending.",
				Count: func(context.Context) (int, error) { return 3, nil }},
			Handler: noopHandler},
		{Path: "leads", NavLabel: "Leads", Permission: "leads", AdminsNeedGrant: true,
			NavCount: func(context.Context) (int, error) { return 9, nil },
			Dashboard: &DashboardCard{Description: "Submissions.",
				Note: func(context.Context) (string, error) { return "Newest: moments ago", nil }},
			Handler: noopHandler},
		{Path: "no-card", NavLabel: "No card", Handler: noopHandler},
		{Path: "broken", NavLabel: "Broken",
			Dashboard: &DashboardCard{
				Count: func(context.Context) (int, error) { return 0, errors.New("boom") },
				Note:  func(context.Context) (string, error) { return "", errors.New("boom") }},
			Handler: noopHandler},
	}
	s := dashTestServer(sections)
	ctx := context.Background()

	titles := func(cards []dashCard) string {
		var names []string
		for _, c := range cards {
			names = append(names, c.Title)
		}
		return strings.Join(names, ",")
	}

	// A superadmin sees every card, in registration order; a section
	// without a Dashboard gets none.
	super := s.dashCards(ctx, &auth.User{Role: auth.RoleSuperadmin})
	if got := titles(super); got != "Vehicles,Leads,Broken" {
		t.Fatalf("superadmin cards = %q, want Vehicles,Leads,Broken", got)
	}
	if super[0].URL != "/admin/x/inventory/" {
		t.Errorf("URL = %q, want the section root", super[0].URL)
	}
	if super[0].Count != 3 || !super[0].HasCount {
		t.Errorf("Vehicles count = %d (has %v), want the card's own Count (3) over NavCount", super[0].Count, super[0].HasCount)
	}
	if super[1].Count != 9 || !super[1].HasCount {
		t.Errorf("Leads count = %d (has %v), want the NavCount fallback (9)", super[1].Count, super[1].HasCount)
	}
	if super[2].Count != 0 || !super[2].HasCount {
		t.Errorf("Broken count = %d (has %v), want a failed count rendered as zero", super[2].Count, super[2].HasCount)
	}
	if super[1].Note != "Newest: moments ago" {
		t.Errorf("Leads note = %q, want the Note func's line", super[1].Note)
	}
	if super[0].Note != "" || super[2].Note != "" {
		t.Errorf("notes = %q, %q; want empty without a Note func and on a failed one", super[0].Note, super[2].Note)
	}

	// Cards follow the sections' visibility rules: an editor sees only
	// what their grants open, and "leads" binds admins too.
	editor := &auth.User{Role: auth.RoleEditor, Permissions: []auth.Permission{"vehicles"}}
	if got := titles(s.dashCards(ctx, editor)); got != "Vehicles,Broken" {
		t.Errorf("editor cards = %q, want Vehicles,Broken", got)
	}
	admin := &auth.User{Role: auth.RoleAdmin}
	if got := titles(s.dashCards(ctx, admin)); got != "Vehicles,Broken" {
		t.Errorf("ungranted admin cards = %q, want Vehicles,Broken (leads needs the grant)", got)
	}
	admin.Permissions = []auth.Permission{"leads"}
	if got := titles(s.dashCards(ctx, admin)); got != "Vehicles,Leads,Broken" {
		t.Errorf("granted admin cards = %q, want all three", got)
	}
}

func TestDashboardTemplateVisibility(t *testing.T) {
	tmpl := parseTemplates()["dashboard"]
	render := func(data templateData) string {
		var sb strings.Builder
		if err := tmpl.ExecuteTemplate(&sb, "layout", data); err != nil {
			t.Fatalf("rendering dashboard: %v", err)
		}
		return sb.String()
	}
	// Matched as cards specifically: the sidebar links to the same
	// sections for anyone who may open them, and that is not what this
	// test is about.
	builtins := []string{
		`class="cms-card" href="/admin/pages"`,
		`class="cms-card" href="/admin/snippets"`,
		`class="cms-card" href="/admin/users"`,
		`class="cms-card" href="/admin/media"`,
	}

	// An editor's dashboard: their section cards, no built-in cards, no
	// Blog & News without a feed grant.
	data := templateData{
		AdminPath:    "/admin",
		User:         &auth.User{Name: "Pat", Role: auth.RoleEditor},
		PagesEnabled: true, PostsEnabled: true, MediaEnabled: true,
		DashCards: []dashCard{{URL: "/admin/x/inventory/", Title: "Vehicles", Description: "Pending.",
			Note: "Oldest undelivered: 2 days", Count: 3, HasCount: true}},
	}
	postsCard := `class="cms-card" href="/admin/posts"`
	out := render(data)
	if !strings.Contains(out, `href="/admin/x/inventory/"`) || !strings.Contains(out, "Vehicles") {
		t.Error("editor dashboard is missing the host section card")
	}
	if !strings.Contains(out, "Oldest undelivered: 2 days") {
		t.Error("editor dashboard is missing the card's note line")
	}
	for _, link := range append(builtins, postsCard) {
		if strings.Contains(out, link) {
			t.Errorf("editor dashboard shows %s, want it hidden", link)
		}
	}

	// The blogs grant opens Blog & News, and nothing else.
	data.User.Permissions = []auth.Permission{auth.PermBlogs}
	if out := render(data); !strings.Contains(out, postsCard) {
		t.Error("blogs grant does not show the Blog & News card")
	}

	// A superadmin sees the built-in cards too.
	data.User = &auth.User{Name: "Sam", Role: auth.RoleSuperadmin}
	out = render(data)
	for _, link := range append(builtins, postsCard) {
		if !strings.Contains(out, link) {
			t.Errorf("superadmin dashboard is missing %s", link)
		}
	}
}

func TestDashboardTemplateTraffic(t *testing.T) {
	tmpl := parseTemplates()["dashboard"]
	data := templateData{
		AdminPath: "/admin",
		User:      &auth.User{Name: "Pat", Role: auth.RoleEditor},
	}

	// No chart data at all (nil store, failed query): no Traffic section.
	var sb strings.Builder
	if err := tmpl.ExecuteTemplate(&sb, "layout", data); err != nil {
		t.Fatalf("rendering dashboard: %v", err)
	}
	if strings.Contains(sb.String(), "cms-traffic") {
		t.Error("dashboard renders a traffic section with no Traffic data")
	}

	// Views recorded: an SVG with a bar and its tooltip, and the top
	// pages listed beside it.
	data.Traffic = &trafficChart{
		ViewW: chartW, ViewH: chartH, PlotX: chartLeft, PlotX2: chartRight,
		TickX: chartLeft - 8, Baseline: chartBaseline, Total: 5, HasViews: true,
		TopPages: []content.PathViews{{Path: "/used-cars", Views: 4}, {Path: "/", Views: 1}},
		Ticks:    []trafficTick{{Y: 18, Label: 6}, {Y: 89, Label: 3}},
		Bars: []trafficBar{{
			Path: barPath(50, chartBaseline, 44, 80),
			HitX: 40, HitY: chartTop, HitW: 84, HitH: 100,
			LabelX: 82, LabelY: chartLabelY, Label: "Mon",
			Title: "Aug 10, 2026 — 5 page views", Count: 5,
			CountX: 82, CountY: 74, ShowCount: true,
		}},
	}
	sb.Reset()
	if err := tmpl.ExecuteTemplate(&sb, "layout", data); err != nil {
		t.Fatalf("rendering dashboard with traffic: %v", err)
	}
	out := sb.String()
	for _, want := range []string{"<svg", "cms-traffic-bar", "Aug 10, 2026 — 5 page views", ">Mon</text>",
		"cms-traffic-top", `<a href="/used-cars"`, ">/used-cars</a>"} {
		if !strings.Contains(out, want) {
			t.Errorf("traffic chart is missing %s", want)
		}
	}

	// A table that exists but holds nothing yet: the honest note, no SVG.
	data.Traffic = &trafficChart{HasViews: false}
	sb.Reset()
	if err := tmpl.ExecuteTemplate(&sb, "layout", data); err != nil {
		t.Fatalf("rendering dashboard with empty traffic: %v", err)
	}
	out = sb.String()
	if strings.Contains(out, "<svg") || !strings.Contains(out, "No page views recorded yet") {
		t.Error("empty traffic should render the note instead of a chart")
	}
}

func TestNiceCeil(t *testing.T) {
	cases := map[int]int{
		0: 2, 1: 2, 2: 2, 3: 4, 5: 6, 7: 8, 9: 10, 10: 10,
		11: 20, 45: 60, 85: 100, 101: 200, 999: 1000, 1000: 1000,
	}
	for n, want := range cases {
		if got := niceCeil(n); got != want {
			t.Errorf("niceCeil(%d) = %d, want %d", n, got, want)
		}
	}
	// Every axis top must halve to a whole number for the midline label.
	for n := 0; n <= 5000; n++ {
		if niceCeil(n)%2 != 0 {
			t.Fatalf("niceCeil(%d) = %d is odd; the midline label would be a fraction", n, niceCeil(n))
		}
	}
}
