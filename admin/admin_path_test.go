package admin

import (
	"regexp"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

// adminURLAttrRE finds the attribute values an admin screen can send a
// browser to: links, form targets, assets, and the data-* URL templates
// the admin scripts expand.
var adminURLAttrRE = regexp.MustCompile(`(?:href|action|src|formaction|data-[a-z-]+)="([^"]*)"`)

// Every screen builds its links from AdminPath. Rendering them all under
// a moved admin and finding the default prefix anywhere means a template
// hardcoded it, and that link is broken on every site that moved.
func TestTemplatesLinkUnderAdminPath(t *testing.T) {
	const adminPath = "/cms-admin"
	user := &auth.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: auth.RoleSuperadmin}
	page := &content.Page{ID: 3, Title: "About", Slug: "about"}
	post := &content.Post{Page: content.Page{ID: 4, Title: "Hello", Slug: "blog/hello"}, PostID: 5, Feed: "blog"}
	snip := &snippets.Snippet{ID: 6, Name: "Hero"}
	img := media.View{Media: media.Media{ID: 8, Kind: media.KindImage, Filename: "a.png"}}
	folder := media.Folder{ID: 9, Kind: media.KindImage, Name: "Shots", Count: 1}

	data := templateData{
		AdminPath:      adminPath,
		AdminLang:      "en",
		User:           user,
		MasqueradeFrom: user,
		FormUser:       user,
		Users:          []userRow{{User: *user, CanManage: true}},
		PagesEnabled:   true,
		Pages:          []content.Page{*page},
		FormPage:       page,
		PostsEnabled:   true,
		Posts:          []content.Post{*post},
		FormPost:       post,
		Snippets:       []snippets.Snippet{*snip},
		FormSnippet:    snip,
		SectionStyles:  render.DefaultSectionStyles(),
		MediaEnabled:   true,
		Media:          []media.View{img},
		Entries:        []media.View{img},
		Folders:        []media.Folder{folder},
		CurrentFolder:  &folder,
		MediaTab:       "images",
		MediaKind:      "image",
		Locales:        []string{"en", "fr"},
		EditLocale:     "en",
		ForgotEnabled:  true,
		PageScript:     "media.js",
		PagerCSSPath:   "/static/pager.css",
	}

	for name, tpl := range parseTemplates() {
		var b strings.Builder
		if err := tpl.ExecuteTemplate(&b, "layout", data); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		urls := 0
		for _, m := range adminURLAttrRE.FindAllStringSubmatch(b.String(), -1) {
			u := m[1]
			if u == "/admin" || strings.HasPrefix(u, "/admin/") || strings.HasPrefix(u, "/admin?") {
				t.Errorf("%s: %q ignores AdminPath", name, u)
			}
			if strings.HasPrefix(u, adminPath+"/") {
				urls++
			}
		}
		if urls == 0 {
			t.Errorf("%s: rendered no links under %s — is the fixture reaching the template?", name, adminPath)
		}
	}
}

// A page at or under AdminPath is one Handler never hands to the public
// site, so the admin must not create it.
func TestAdminSlugCollision(t *testing.T) {
	for _, tc := range []struct {
		adminPath, slug string
		want            bool
	}{
		{"/admin", "admin", true},
		{"/admin", "admin/tools", true},
		{"/admin", "administrator", false},
		{"/admin", "about", false},
		{"/cms-admin", "cms-admin", true},
		{"/cms-admin", "admin", false},
		{"/site/manage", "site/manage/x", true},
		{"/site/manage", "site", false},
		{"/site/manage", "site/managers", false},
		{"/Manage", "manage", false}, // request paths are case-sensitive
	} {
		s := &server{deps: Deps{AdminPath: tc.adminPath}}
		if got := s.adminSlugCollision(tc.slug); got != tc.want {
			t.Errorf("AdminPath %s, slug %q: collision = %v, want %v", tc.adminPath, tc.slug, got, tc.want)
		}
	}
}
