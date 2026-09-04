package content

// Page history. A version is one edition of a page: the whole of its
// published content — every region, every locale, plus the page-level fields
// that take part in the draft/publish workflow — frozen as a JSON document
// at the moment it went live.
//
// Whole-page rather than per-block, because that is the unit Publish already
// works in: it is locale-blind, a sections reorder rewrites every block in
// the region, and a partial rollback of a sections stack does not mean
// anything. A version is exactly the set of rows Publish copies.
//
// Publish writes an edition as it makes a page live (see Store.PublishAs),
// which is the only thing that creates history today. See
// migrations/sql/postgres/0033_page_versions.sql for the storage decisions
// and for what a version deliberately leaves out.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/snippets"
)

// snapshotVersion is the payload format's own version number, stored in
// every snapshot. History outlives the code that wrote it, so a payload has
// to say which shape it is.
//
//	1 — page, meta, blocks.
//	2 — adds the custom-code blocks the page's markup refers to.
const snapshotVersion = 2

// DefaultVersionsKept is how many editions of a page PruneVersions keeps
// when the caller names no limit. Payloads hold a page's entire content, so
// history is not free; fifty editions is far more than anyone scrolls
// through and still bounds a busy page's share of the table.
const DefaultVersionsKept = 50

// VersionKind is why a version was taken.
type VersionKind string

const (
	// VersionPublish is an edition captured as the page went live. Every
	// version is one of these until manual snapshots exist.
	VersionPublish VersionKind = "publish"
	// VersionManual is a snapshot someone asked for by hand.
	VersionManual VersionKind = "manual"
)

// orPublish maps a zero VersionKind to the default, so callers that never
// set the field write a valid value — the same courtesy Visibility.orPublic
// does for pages.
func (k VersionKind) orPublish() VersionKind {
	if k == "" {
		return VersionPublish
	}
	return k
}

// Version is one entry in a page's history without its content: what the
// list screen shows. The payload is deliberately absent, so listing a page
// with fifty editions does not drag fifty page-sized documents out of the
// database to render fifty dates.
type Version struct {
	ID     int64
	PageID int64
	Kind   VersionKind
	Note   string
	// SavedBy is the account that published this edition, nil when it is
	// unknown (published by something other than a person) or when the
	// account has since been deleted.
	SavedBy *int64
	// SavedByName is that account's name, resolved from cms_users. Empty
	// when SavedBy is nil, and also when the user is gone — history
	// survives the account, so a version whose author was deleted still
	// lists, just without a name.
	SavedByName string
	SavedAt     time.Time
}

// Snapshot is a page's published content frozen at one moment: the JSON
// document stored in cms_page_versions.payload.
//
// The field names are the payload's wire format. They are stable data on
// disk, not an internal struct — renaming one silently orphans every
// version already written — so they are spelled out rather than defaulted.
type Snapshot struct {
	V      int             `json:"v"`
	Page   SnapshotPage    `json:"page"`
	Meta   []SnapshotMeta  `json:"meta"`
	Blocks []SnapshotBlock `json:"blocks"`
	// Code is the custom-code library entries the blocks above refer to,
	// frozen with them and ordered by key. A page's markup holds only an
	// inert placeholder naming a key (see snippets.CodeSnippet), so
	// without this an edition would restore a page whose widgets had
	// since been rewritten — or, where the key was deleted, whose widgets
	// were gone. Absent in format 1, and on a page that names none.
	Code []SnapshotCode `json:"code,omitempty"`
}

// SnapshotCode is one custom-code library entry as a page depended on it.
//
// It is a copy of something the page does not own: the library is shared,
// and two pages can name the same key. That is why restoring puts one back
// only when the library no longer holds it — see RestoreVersion.
type SnapshotCode struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	HTML string `json:"html"`
}

// SnapshotPage holds the page-level fields that take part in the
// draft/publish workflow — the three cms_page_drafts stages and Publish
// copies onto cms_pages. Slug and visibility are not among them: 0021 kept
// those immediate because they are addressing rather than content.
type SnapshotPage struct {
	TemplateName string `json:"template_name"`
	HeadCSS      string `json:"head_css"`
	BodyJS       string `json:"body_js"`
}

// SnapshotMeta is one locale's published page metadata.
type SnapshotMeta struct {
	Locale          string `json:"locale"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	MetaDescription string `json:"meta_description"`
}

// SnapshotBlock is one published block. It carries no id: block ids identify
// live rows, and a restore writes new ones, so recording them would freeze a
// number that means nothing by the time anyone reads it.
type SnapshotBlock struct {
	Region     string            `json:"region"`
	Locale     string            `json:"locale"`
	Sort       int               `json:"sort"`
	Kind       Kind              `json:"kind"`
	SnippetKey *string           `json:"snippet_key,omitempty"`
	Content    string            `json:"content"`
	Settings   map[string]string `json:"settings,omitempty"`
}

// handle is the part of a database handle these helpers use. Both *sqldb.DB
// and *sqldb.Tx satisfy it, so a snapshot can be taken on its own or inside
// the transaction that publishes the page — which is where it will be taken
// once Publish writes history, so that a failed publish leaves no edition
// behind claiming to have gone live.
type handle interface {
	Exec(ctx context.Context, query string, args ...any) (sqldb.Result, error)
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
	InsertID(ctx context.Context, query string, args ...any) (int64, error)
}

// PublishedSnapshot freezes a page's currently published content — what the
// site is serving — without storing anything. It is the read behind
// SaveVersion, and on its own it is how a caller compares an old edition
// against what is live.
func (s *Store) PublishedSnapshot(ctx context.Context, pageID int64) (*Snapshot, error) {
	return publishedSnapshot(ctx, s.db, pageID)
}

// publishedSnapshot reads the published side of the three tables Publish
// writes. Every read is ordered on the columns that uniquely identify its
// rows, so the same content always marshals to the same bytes: that is what
// makes SaveVersion's hash comparison mean "unchanged" rather than "the
// database happened to return these in the same order twice".
func publishedSnapshot(ctx context.Context, h handle, pageID int64) (*Snapshot, error) {
	snap := &Snapshot{V: snapshotVersion}

	// The published page-level values live on cms_pages itself; the working
	// copy in cms_page_drafts is the unpublished side and has no business in
	// a record of what went live.
	err := h.QueryRow(ctx,
		"SELECT template_name, head_css, body_js FROM cms_pages WHERE id = $1", pageID).
		Scan(&snap.Page.TemplateName, &snap.Page.HeadCSS, &snap.Page.BodyJS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	rows, err := h.Query(ctx, `
		SELECT locale, title, description, meta_description
		FROM cms_page_meta
		WHERE page_id = $1 AND status = 'published'
		ORDER BY locale`, pageID)
	if err != nil {
		return nil, err
	}
	snap.Meta, err = sqldb.CollectRows(rows, func(row sqldb.Scanner) (SnapshotMeta, error) {
		var m SnapshotMeta
		err := row.Scan(&m.Locale, &m.Title, &m.Description, &m.MetaDescription)
		return m, err
	})
	if err != nil {
		return nil, err
	}

	rows, err = h.Query(ctx, `
		SELECT region, locale, sort, kind, snippet_key, content, settings
		FROM cms_blocks
		WHERE page_id = $1 AND status = 'published'
		ORDER BY region, locale, sort`, pageID)
	if err != nil {
		return nil, err
	}
	snap.Blocks, err = sqldb.CollectRows(rows, func(row sqldb.Scanner) (SnapshotBlock, error) {
		var b SnapshotBlock
		err := row.Scan(&b.Region, &b.Locale, &b.Sort, &b.Kind, &b.SnippetKey,
			&b.Content, sqldb.JSONInto(&b.Settings))
		return b, err
	})
	if err != nil {
		return nil, err
	}

	snap.Code, err = frozenCode(ctx, h, snap.Blocks)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// frozenCode reads the custom-code library entries the blocks refer to,
// ordered by key so the payload's bytes stay stable.
//
// One consequence worth knowing: a page's payload changes when a code
// block it uses is rewritten, even though the page itself did not change,
// so the next publish of that page records a new edition. That is honest —
// what goes live for the page really is different — but it is not
// something HasUnpublishedChanges reports, because code is not staged at
// all: editing a block is live everywhere the moment it is saved, and the
// publish merely writes down what the page now depends on.
func frozenCode(ctx context.Context, h handle, blocks []SnapshotBlock) ([]SnapshotCode, error) {
	seen := map[string]bool{}
	var keys []string
	for _, b := range blocks {
		for _, key := range snippets.CodeKeysIn(b.Content) {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	// A placeholder list rather than a fixed one: a page names as many
	// blocks as it names, and the dialect rewrites the whole statement.
	args := make([]any, len(keys))
	marks := make([]string, len(keys))
	for i, key := range keys {
		args[i] = key
		marks[i] = "$" + strconv.Itoa(i+1)
	}
	rows, err := h.Query(ctx, `
		SELECT code_key, name, html FROM cms_code_snippets
		WHERE code_key IN (`+strings.Join(marks, ", ")+`)
		ORDER BY code_key`, args...)
	if err != nil {
		return nil, err
	}
	// A key with no row is a placeholder naming a block that does not
	// exist, which renders as nothing. There is no code to freeze, and the
	// page is already showing what it will show after a restore.
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (SnapshotCode, error) {
		var c SnapshotCode
		err := row.Scan(&c.Key, &c.Name, &c.HTML)
		return c, err
	})
}

// saveVersion records the page's currently published content as a new
// edition in its history, attributed to by (nil when no account is
// responsible). It returns the new version's id, or 0 when nothing was
// written because the snapshot matches the newest one already stored.
//
// That skip is not an optimization. Publishing any page publishes the site's
// shared content along with it, so the __site page would otherwise gain an
// identical edition every time anyone published anything.
//
// It is deliberately not exported. Publishing is what makes an edition, and
// it calls this inside its own transaction; a caller outside that path has
// by definition nothing new to record, so an exported version of this would
// be a method that almost always did nothing.
func saveVersion(ctx context.Context, h handle, pageID int64, kind VersionKind, note string, by *int64) (int64, error) {
	snap, err := publishedSnapshot(ctx, h, pageID)
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(snap)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])

	// Read the newest hash and compare here rather than folding the check
	// into the insert: an INSERT ... SELECT cannot report its generated id
	// portably — MySQL has no RETURNING and MariaDB supports it only on the
	// VALUES form — which is the same reason Duplicate reads before it
	// writes. Two callers racing can both pass this and store the same
	// edition twice; the cost is a duplicate row in a list, and once
	// Publish takes the snapshot inside its own transaction the page row it
	// already updates serializes them anyway.
	var latest string
	err = h.QueryRow(ctx, `
		SELECT payload_hash FROM cms_page_versions
		WHERE page_id = $1
		ORDER BY id DESC
		LIMIT 1`, pageID).Scan(&latest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if latest == hash {
		return 0, nil
	}

	return h.InsertID(ctx, `
		INSERT INTO cms_page_versions (page_id, payload, payload_hash, kind, note, saved_by)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		pageID, string(payload), hash, kind.orPublish(), note, by)
}

// Versions lists a page's history newest first, without the payloads. A page
// with no history — one never published, or an install that predates
// history — comes back empty rather than as an error.
func (s *Store) Versions(ctx context.Context, pageID int64) ([]Version, error) {
	rows, err := s.db.Query(ctx, `
		SELECT v.id, v.page_id, v.kind, v.note, v.saved_by, COALESCE(u.name, ''), v.saved_at
		FROM cms_page_versions v
		LEFT JOIN cms_users u ON u.id = v.saved_by
		WHERE v.page_id = $1
		ORDER BY v.id DESC`, pageID)
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (Version, error) {
		var v Version
		err := row.Scan(&v.ID, &v.PageID, &v.Kind, &v.Note, &v.SavedBy, &v.SavedByName, &v.SavedAt)
		return v, err
	})
}

// VersionSnapshot returns one edition of a page: its metadata and the
// content frozen in it. ErrNotFound when no such version exists.
//
// It takes the page id as well as the version id, and matches on both, so
// that a version id belonging to another page cannot be read — or, later,
// restored — through a route scoped to this one.
func (s *Store) VersionSnapshot(ctx context.Context, pageID, versionID int64) (*Version, *Snapshot, error) {
	var v Version
	var payload string
	err := s.db.QueryRow(ctx, `
		SELECT v.id, v.page_id, v.kind, v.note, v.saved_by, COALESCE(u.name, ''), v.saved_at, v.payload
		FROM cms_page_versions v
		LEFT JOIN cms_users u ON u.id = v.saved_by
		WHERE v.id = $1 AND v.page_id = $2`, versionID, pageID).
		Scan(&v.ID, &v.PageID, &v.Kind, &v.Note, &v.SavedBy, &v.SavedByName, &v.SavedAt, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	snap, err := decodeSnapshot(versionID, payload)
	if err != nil {
		return nil, nil, err
	}
	return &v, snap, nil
}

// decodeSnapshot reads a stored payload.
//
// An older format reads straight into the current struct: every change so
// far has been an added field, so what an old payload does not carry comes
// back as its zero value, which is what "this edition predates that
// feature" should mean. A change that is not additive — a renamed or
// reshaped field — needs a real branch here rather than this rule.
//
// A payload from a *newer* format is refused. It is a real possibility — a
// rollback of the application over a database a later build has already
// written to — and reading it with the current struct would quietly drop
// whatever it added, which on a restore means losing content rather than
// failing.
func decodeSnapshot(versionID int64, payload string) (*Snapshot, error) {
	var snap Snapshot
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		return nil, fmt.Errorf("content: version %d payload: %w", versionID, err)
	}
	if snap.V < 1 {
		return nil, fmt.Errorf("content: version %d payload names no snapshot format", versionID)
	}
	if snap.V > snapshotVersion {
		return nil, fmt.Errorf("content: version %d was saved in snapshot format %d, and this build reads format %d",
			versionID, snap.V, snapshotVersion)
	}
	return &snap, nil
}

// PruneVersions deletes all but the newest keep editions of a page. A keep
// of zero or less means DefaultVersionsKept. Deleting nothing — a page with
// fewer editions than that — is not an error.
func (s *Store) PruneVersions(ctx context.Context, pageID int64, keep int) error {
	return pruneVersions(ctx, s.db, pageID, keep)
}

func pruneVersions(ctx context.Context, h handle, pageID int64, keep int) error {
	if keep <= 0 {
		keep = DefaultVersionsKept
	}
	// Find the oldest edition worth keeping, then delete everything below
	// it. One statement would need a subquery over cms_page_versions inside
	// a DELETE against cms_page_versions, which MySQL rejects outright; two
	// statements say the same thing on every engine and let the delete run
	// as an index range scan.
	//
	// The offset is written into the SQL rather than bound: it is an int
	// this function has already clamped, and placeholders in OFFSET are the
	// one spot in the module where the three engines' prepared-statement
	// support has not been exercised.
	var cutoff int64
	err := h.QueryRow(ctx, `
		SELECT id FROM cms_page_versions
		WHERE page_id = $1
		ORDER BY id DESC
		LIMIT 1 OFFSET `+strconv.Itoa(keep-1), pageID).Scan(&cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // fewer editions than keep: nothing to drop
	}
	if err != nil {
		return err
	}
	_, err = h.Exec(ctx,
		"DELETE FROM cms_page_versions WHERE page_id = $1 AND id < $2", pageID, cutoff)
	return err
}

// settingsOrEmpty is a block's settings map made safe to store. The
// snapshot omits an empty one to keep payloads small, so it decodes as nil
// — and cms_blocks.settings is NOT NULL.
func settingsOrEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// BlocksFor returns an edition's blocks for one locale, shaped as the live
// blocks a renderer takes, with the same region-level fallback to
// defaultLocale that EffectiveBlocks applies: a region with no rows in the
// requested locale reads as the default locale's wholesale. It is how a
// stored edition is previewed through the real site templates.
//
// pageID is stamped on each block because a snapshot does not record one —
// it is content, and the page it belongs to is the row that holds it.
func (snap *Snapshot) BlocksFor(pageID int64, locale, defaultLocale string) []Block {
	localized := map[string]bool{}
	if locale != defaultLocale {
		for _, b := range snap.Blocks {
			if b.Locale == locale {
				localized[b.Region] = true
			}
		}
	}
	var out []Block
	for _, b := range snap.Blocks {
		if b.Locale != locale && b.Locale != defaultLocale {
			continue
		}
		// A default-locale row is dropped only when this region has the
		// requested locale's own rows to show instead.
		if b.Locale != locale && localized[b.Region] {
			continue
		}
		out = append(out, Block{
			PageID:     pageID,
			Region:     b.Region,
			Locale:     b.Locale,
			Status:     StatusPublished,
			Sort:       b.Sort,
			Kind:       b.Kind,
			SnippetKey: b.SnippetKey,
			Content:    b.Content,
			Settings:   settingsOrEmpty(b.Settings),
		})
	}
	return out
}

// MetaFor returns an edition's metadata for one locale with the same
// field-level fallback the live read applies: an absent row, or an empty
// field within one, falls back to defaultLocale, so an edition of a French
// page that only ever had a title still previews with the English
// description.
func (snap *Snapshot) MetaFor(locale, defaultLocale string) SnapshotMeta {
	var want, fallback SnapshotMeta
	for _, m := range snap.Meta {
		switch m.Locale {
		case locale:
			want = m
		case defaultLocale:
			fallback = m
		}
	}
	if want.Title == "" {
		want.Title = fallback.Title
	}
	if want.Description == "" {
		want.Description = fallback.Description
	}
	if want.MetaDescription == "" {
		want.MetaDescription = fallback.MetaDescription
	}
	want.Locale = locale
	return want
}

// RestoreResult reports what a restore did beyond the page's own content:
// what became of the custom-code blocks the edition depended on. Both
// lists are ordered by key, and both are empty on the ordinary restore of
// a page whose blocks are all still exactly as they were.
type RestoreResult struct {
	// CodeRecreated names the blocks the edition carried that the library
	// no longer held, and which were put back from it. Recreating one is
	// safe precisely because nothing else can be using it: it was gone.
	CodeRecreated []string
	// CodeChanged names the blocks the library still holds under a
	// different body. They are left alone — the library is shared, and a
	// button labelled "restore this page" must not rewrite a widget that
	// other pages show — so this is the list to tell someone about rather
	// than to act on.
	CodeChanged []string
}

// RestoreVersion replaces a page's draft content with the edition
// versionID: its blocks, its metadata for every locale, and the page-level
// fields it staged. ErrNotFound when the version does not exist or belongs
// to another page.
//
// It writes the working copy and nothing else, which is exactly what
// DiscardDraft does with the published rows as its source — the page's
// publication status, and what the site is serving, are untouched until
// someone publishes. That is the point: a rollback is reviewable, the
// editor and the preview show it first, and nothing reaches the public
// site by way of a button labelled "restore".
//
// The current draft is overwritten. A caller with unpublished edits in
// hand should say so before calling this; HasUnpublishedChanges is the
// question to ask.
//
// The custom-code blocks the edition depended on are handled as described
// on RestoreResult: put back when the library lost them, left alone when
// it still has them under some other body. Everything else the page points
// at — media, palette snippets, the site's shared regions — belongs to
// somebody else and is not touched at all.
func (s *Store) RestoreVersion(ctx context.Context, pageID, versionID int64) (*RestoreResult, error) {
	_, snap, err := s.VersionSnapshot(ctx, pageID, versionID)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		"DELETE FROM cms_blocks WHERE page_id = $1 AND status = 'draft'", pageID); err != nil {
		return nil, err
	}
	for _, b := range snap.Blocks {
		if _, err := tx.Exec(ctx, `
			INSERT INTO cms_blocks (page_id, region, locale, status, sort, kind, snippet_key, content, settings)
			VALUES ($1, $2, $3, 'draft', $4, $5, $6, $7, $8)`,
			pageID, b.Region, b.Locale, b.Sort, b.Kind, b.SnippetKey, b.Content,
			sqldb.JSON(settingsOrEmpty(b.Settings))); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(ctx,
		"DELETE FROM cms_page_meta WHERE page_id = $1 AND status = 'draft'", pageID); err != nil {
		return nil, err
	}
	for _, m := range snap.Meta {
		if _, err := tx.Exec(ctx, `
			INSERT INTO cms_page_meta (page_id, locale, title, description, meta_description, status)
			VALUES ($1, $2, $3, $4, $5, 'draft')`,
			pageID, m.Locale, m.Title, m.Description, m.MetaDescription); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_drafts (page_id, template_name, head_css, body_js)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (page_id)
		DO UPDATE SET template_name = EXCLUDED.template_name,
			head_css = EXCLUDED.head_css, body_js = EXCLUDED.body_js`,
		pageID, snap.Page.TemplateName, snap.Page.HeadCSS, snap.Page.BodyJS); err != nil {
		return nil, err
	}

	out, err := restoreCode(ctx, tx, snap.Code)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// restoreCode puts back the custom-code blocks the library has lost and
// reports the ones it still holds differently. See RestoreResult for why
// the second group is reported rather than rewritten.
func restoreCode(ctx context.Context, h handle, code []SnapshotCode) (*RestoreResult, error) {
	out := &RestoreResult{}
	for _, c := range code {
		var live string
		err := h.QueryRow(ctx,
			"SELECT html FROM cms_code_snippets WHERE code_key = $1", c.Key).Scan(&live)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := h.Exec(ctx,
				"INSERT INTO cms_code_snippets (code_key, name, html) VALUES ($1, $2, $3)",
				c.Key, c.Name, c.HTML); err != nil {
				return nil, err
			}
			out.CodeRecreated = append(out.CodeRecreated, c.Key)
			continue
		}
		if live != c.HTML {
			out.CodeChanged = append(out.CodeChanged, c.Key)
		}
	}
	return out, nil
}
