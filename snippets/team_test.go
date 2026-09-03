package snippets

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestTeamPresetIsBuiltFromTheCard(t *testing.T) {
	// The card tool grows a team grid by copying a card that is already
	// there, so the three the section ships with have to be the same
	// markup a hand-added fourth will be — otherwise the first person
	// somebody adds looks subtly unlike their colleagues.
	team := find(t, DefaultSectionPresets(), "Team")
	if n := strings.Count(team.HTML, teamCardHTML); n != 3 {
		t.Errorf("Team preset is built from %d copies of teamCardHTML, want 3", n)
	}
	inline := find(t, DefaultSnippets(), "Team cards")
	if inline.HTML != team.HTML {
		t.Error("the inline snippet and the section preset are different markup")
	}
}

// TestTeamGridWrapsRatherThanSqueezing is the requirement the whole
// design turns on: how many cards there are and how many fit on a line
// are different numbers.
func TestTeamGridWrapsRatherThanSqueezing(t *testing.T) {
	team := find(t, DefaultSectionPresets(), "Team")
	// One column on a phone, and a ceiling at each breakpoint above it.
	// A grid with a track count and more children than tracks wraps on
	// its own — that is the whole mechanism, and it is why nothing in
	// the editor has to renumber anything when a fourth person joins.
	for _, cls := range []string{"grid-cols-1", "sm:grid-cols-2", "lg:grid-cols-3"} {
		if !strings.Contains(team.HTML, cls) {
			t.Errorf("the team grid is missing %q — without the ladder it is not responsive", cls)
		}
	}
	if strings.Contains(team.HTML, "sm:grid-cols-3") {
		t.Error("three across at the small breakpoint squeezes portraits on a tablet")
	}
}

// TestTeamGridsAreMarkedForTheCardTool guards the contract with
// columns.js: a grid that both tools could claim gets edited by two
// tools writing the same grid-cols class.
func TestTeamGridsAreMarkedForTheCardTool(t *testing.T) {
	presets := append(DefaultSectionPresets(), LibrarySectionPresets()...)
	for _, name := range []string{"Team", "Team profiles"} {
		s := find(t, presets, name)
		if !strings.Contains(s.HTML, `class="cms-team `) {
			t.Errorf("%q: the grid has no cms-team marker, so the column tool will claim it", name)
		}
		// Every child of the grid, or the tool's run-of-adjacent-cards
		// walk stops at the first unmarked one.
		cards := strings.Count(s.HTML, "cms-team-card")
		if cards != 3 {
			t.Errorf("%q: %d cards carry cms-team-card, want 3", name, cards)
		}
	}
}

// TestTeamPlaceholdersMatchTheCardTool is the one cross-language
// coupling in this feature, and it fails silently without a check: the
// tool reads these strings back to tell a card nobody has touched from
// one somebody wrote, and a card it cannot recognise prompts before
// every delete — which teaches an editor to click through the prompt.
func TestTeamPlaceholdersMatchTheCardTool(t *testing.T) {
	src, err := os.ReadFile("../editor/src/team.js")
	if err != nil {
		t.Fatalf("reading the card tool: %v", err)
	}
	js := string(src)
	// The card's markup is HTML and the tool compares against
	// textContent, so the entities have to be resolved first. Only the
	// ones teamCardHTML actually uses.
	text := strings.NewReplacer("&mdash;", "—", "&amp;", "&").Replace(teamCardHTML)

	for _, name := range []string{"PLACEHOLDER_NAME", "PLACEHOLDER_TITLE", "PLACEHOLDER_BIO"} {
		want := jsString(t, js, name)
		if !strings.Contains(text, ">"+want+"<") {
			t.Errorf("team.js %s = %q, which is not the text of any element in teamCardHTML", name, want)
		}
	}
}

// jsString pulls the value of a `var NAME = "..." + "...";` declaration
// out of the tool's source. Deliberately tiny and deliberately strict:
// it understands the one form the file uses, and fails loudly rather
// than quietly matching nothing if that form ever changes.
func jsString(t *testing.T, js, name string) string {
	t.Helper()
	decl := regexp.MustCompile(`(?s)\bvar ` + name + ` = (.*?);\n`).FindStringSubmatch(js)
	if decl == nil {
		t.Fatalf("no `var %s = ...;` in team.js", name)
	}
	parts := regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`).FindAllStringSubmatch(decl[1], -1)
	if parts == nil {
		t.Fatalf("`var %s` in team.js is not a string literal: %s", name, decl[1])
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p[1])
	}
	return b.String()
}
