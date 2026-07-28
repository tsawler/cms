# mariadb example

A second reference host application, running the CMS on **MariaDB** instead
of Postgres.

Where [`examples/basic`](../basic) is the full setup — Tailwind, the media
library, a login CAPTCHA, blog & news, a custom admin section — this one is
the opposite: the smallest host that does something real. **No build step, no
Node, no object store.** The stylesheet is a `<style>` block in
`templates/base.gohtml`.

## Run it

```sh
docker compose up -d     # MariaDB on localhost:3309
go run .
```

Then open <http://localhost:4200/admin/> and log in with
`admin@example.com` / `password123`.

There is no content yet: create a page from **Pages → New**, leaving the slug
empty to make it the homepage. Then visit `/`, click **Edit** in the bar at
the bottom, and change the text in place.

To start over with an empty database:

```sh
docker compose rm -sfv mariadb && docker compose up -d
```

## What this example shows

**MariaDB needs four DSN settings.** They are in `defaultDSN` in `main.go`,
each with a comment explaining what breaks without it. The one that bites
hardest is `clientFoundRows=true`: without it MySQL and MariaDB report rows
*changed* rather than rows *matched*, so re-saving a record with unchanged
values looks to the CMS like "no such row" and the save fails.

**`Config.Dialect` must be set explicitly.** `database/sql` does not expose
which driver a pool was opened with, so the CMS cannot detect it. `"mysql"`
covers MariaDB too — they share one dialect.

**A host without Tailwind owns its own classes.** The built-in
`SectionStyles` and `EditorStyles` are Tailwind class names, which render as
nothing here. `main.go` overrides both with class names that
`templates/base.gohtml` defines. Worth knowing when you write your own:

| Field | Where it lands |
| --- | --- |
| Background `Class` | the `<section>` wrapper |
| Background `ContentClass` | the inner `<div>` (this is how the built-in dark and accent backgrounds add `prose-invert`) |
| Corner `Class` | the `<section>` wrapper, so the radius clips the background |
| Width `Class` | the inner `<div>` |
| Width `ContentClass` | **unused** |

## Ports

Chosen so every example can run at the same time:

| | Postgres | MySQL | MariaDB | App |
| --- | --- | --- | --- | --- |
| `examples/basic` | 5433 | 3307 | 3308 | 4000 |
| `examples/mariadb` | — | — | 3309 | 4200 |
