# Site search

Full-text search over the published public site: turning it on, the results template, and how the index is maintained.

## Site search

Point the CMS at a results template and it will search the site:

```go
cfg.SearchTemplate = cms.PageTemplate{
    File: "templates/pages/search.gohtml", Label: "Search results",
}
```

That is the whole of turning it on. The CMS then answers at `/search`
(and `/fr/search`, and every other locale), and the site-settings dialog
grows a switch that puts a magnifying glass at the end of the menu bar.
Clicking it drops a search box below the bar; without JavaScript it is an
ordinary link to the search page, which has a box of its own.

**Adding it to a site that already exists.** `cms init` writes
`templates/pages/search.gohtml` and the config line for you, but a site
scaffolded before this feature existed has neither — and until it has
both, there is nothing to switch on: the site-settings dialog says so
rather than showing a switch that would do nothing. Two steps, once:

1. Copy `templates/pages/search.gohtml` from
   [the example site](../examples/basic/templates/pages/search.gohtml) into
   your own `templates/pages/`, and restyle it — it is your markup, the
   same as any other page template.
2. Add `cfg.SearchTemplate` to `main.go`, as above.

Your existing content needs nothing: the index builds itself in the
background the first time the new build serves a request.

**What is searched.** The published words of every public page, post and
news item — the text a visitor reads, with the HTML taken off on the way
in, so a search never matches a class name or the site's own JavaScript.
Drafts, unpublished pages and private pages are not in the index at all,
so nothing that is not on the site can turn up in a result. Shared
regions are left out too: a footer is on every page, and indexing it
would make the whole site match its copyright line. So is anything a
page's template no longer draws — reworking a layout leaves the old
region's content in place, and a search should find what is on the page
rather than what is behind it. Each language is
indexed separately, and a page with no translation is findable in the
other language by the words a visitor would actually be shown there.

**The results template.** There is no stored page behind `/search`, so
this template should not declare editable regions — there is nowhere to
save them. It reads the query and the hits from `{{cmsSearch}}`:

```gotemplate
{{$results := cmsSearch}}
{{cmsSearchForm}}

{{if $results.Searched}}
  <p>{{$results.Total}} results for “{{$results.Query}}”</p>
  {{range $results.Hits}}
    <h2><a href="{{.URL}}">{{.Title}}</a></h2>
    {{if .IsPost}}<p>{{.Kind}} · {{cmsDate .PublishedAt}}</p>{{end}}
    <p>{{with .Summary}}{{.}}{{else}}{{.Snippet}}{{end}}</p>
  {{end}}
  {{cmsPagination $results}}
{{end}}
```

`{{cmsSearch}}` returns one page of results plus the links to the rest —
the same shape `{{cmsFeed}}` has, and `{{cmsPagination}}` draws either.
Each hit carries `.Title`, `.URL`, `.Kind` (`page`, `blog` or `news`),
`.IsPost`, `.PublishedAt` (posts only), the page's own `.Summary`, and a
`.Snippet` cut from around the words that matched. Prefer the summary
when there is one — it was written for a reader deciding whether to click
— and fall back to the snippet. Both are plain text.
`examples/basic/templates/pages/search.gohtml` is a working version of
the above.

`{{cmsSearchForm}}` is the box on its own, for the results page or
anywhere else you want one. It is the same markup and classes as the one
in the menu bar, so one stylesheet dresses both — or write your own form,
a `GET` to the search page with the query in `q`.

**What visitors can type.** Words are AND-ed (`opening hours` finds pages
holding both), `"a quoted phrase"` must appear intact, and a leading `-`
excludes (`hours -weekend`). Anything else is dropped rather than passed
to the database, so a stray bracket is a search that finds nothing rather
than an error page.

**Two more knobs**, both optional:

```go
cfg.SearchPath    = "find" // the address; default "search"
cfg.SearchPerPage = 20     // results per page; default 10
```

A page whose address is the search path wins — the CMS answers there only
when no page does — so a site that built its own search page before this
existed keeps it.

**Nothing to maintain.** The index is written as pages are published and
emptied as they are unpublished or hidden, inside the same transaction,
so it is never out of step with the site. An existing site builds its
index once, in the background, the first time it serves a request after
the upgrade. `(*cms.CMS).ReindexSearch(ctx)` rebuilds it from scratch if
you ever need to; it is safe to run at any time and safe to run twice.
