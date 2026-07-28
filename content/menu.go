package content

import (
	"context"

	"github.com/tsawler/cms/internal/sqldb"
)

// MenuItem is one entry in a navigation menu, with the linked page's slug
// and status joined in for URL resolution and visibility filtering.
type MenuItem struct {
	ID       int64
	Menu     string
	Sort     int
	Label    string            // default-locale label
	Labels   map[string]string // per-locale overrides, e.g. {"fr": "À propos"}
	PageID   *int64
	URL      string
	NewTab   bool
	ParentID *int64

	PageSlug       *string // nil when the item is a literal URL
	PageStatus     *Status
	PageVisibility *Visibility
}

// LabelFor returns the item's label for locale, falling back to the
// default-locale label when no override exists.
func (m MenuItem) LabelFor(locale string) string {
	if l, ok := m.Labels[locale]; ok && l != "" {
		return l
	}
	return m.Label
}

// MenuItems returns menu items ordered by menu and sort. An empty menu
// returns items for every menu (for rendering, which may need several).
func (s *Store) MenuItems(ctx context.Context, menu string) ([]MenuItem, error) {
	q := `
		SELECT mi.id, mi.menu, mi.sort, mi.label, mi.labels, mi.page_id, mi.url, mi.new_tab, mi.parent_id,
		       p.slug, p.status, p.visibility
		FROM cms_menu_items mi
		LEFT JOIN cms_pages p ON p.id = mi.page_id`
	args := []any{}
	if menu != "" {
		q += ` WHERE mi.menu = $1`
		args = append(args, menu)
	}
	q += ` ORDER BY mi.menu, mi.sort`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (MenuItem, error) {
		var m MenuItem
		err := row.Scan(&m.ID, &m.Menu, &m.Sort, &m.Label, sqldb.JSONInto(&m.Labels), &m.PageID, &m.URL, &m.NewTab,
			&m.ParentID, &m.PageSlug, &m.PageStatus, &m.PageVisibility)
		return m, err
	})
}

// MenuItemInput is one entry supplied to ReplaceMenu. A label-only entry
// (no page, no URL) is a dropdown parent; Children go one level deep and
// may not have children of their own.
type MenuItemInput struct {
	Label    string            // default-locale label
	Labels   map[string]string // per-locale overrides; nil stores {}
	PageID   *int64
	URL      string
	NewTab   bool
	Children []MenuItemInput
}

// ReplaceMenu replaces a menu's items with the given ordered tree,
// atomically. Menus have no draft state — changes are live on commit.
func (s *Store) ReplaceMenu(ctx context.Context, menu string, items []MenuItemInput) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "DELETE FROM cms_menu_items WHERE menu = $1", menu); err != nil {
		return err
	}
	// Children share the parent's sort sequence; ordering within a level
	// is what matters, and (menu, sort) stays unique per row for sanity.
	labelsOf := func(in MenuItemInput) map[string]string {
		if in.Labels == nil {
			return map[string]string{}
		}
		return in.Labels
	}
	sort := 0
	for _, item := range items {
		parentID, err := tx.InsertID(ctx, `
			INSERT INTO cms_menu_items (menu, sort, label, labels, page_id, url, new_tab)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			menu, sort, item.Label, sqldb.JSON(labelsOf(item)), item.PageID, item.URL, item.NewTab)
		if err != nil {
			return err
		}
		sort++
		for _, child := range item.Children {
			if _, err := tx.Exec(ctx, `
				INSERT INTO cms_menu_items (menu, sort, label, labels, page_id, url, new_tab, parent_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				menu, sort, child.Label, sqldb.JSON(labelsOf(child)), child.PageID, child.URL, child.NewTab, parentID); err != nil {
				return err
			}
			sort++
		}
	}
	return tx.Commit(ctx)
}
