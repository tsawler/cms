package content

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// MenuItem is one entry in a navigation menu, with the linked page's slug
// and status joined in for URL resolution and visibility filtering.
type MenuItem struct {
	ID       int64
	Menu     string
	Sort     int
	Label    string
	PageID   *int64
	URL      string
	NewTab   bool
	ParentID *int64

	PageSlug   *string // nil when the item is a literal URL
	PageStatus *Status
}

// MenuItems returns menu items ordered by menu and sort. An empty menu
// returns items for every menu (for rendering, which may need several).
func (s *Store) MenuItems(ctx context.Context, menu string) ([]MenuItem, error) {
	q := `
		SELECT mi.id, mi.menu, mi.sort, mi.label, mi.page_id, mi.url, mi.new_tab, mi.parent_id,
		       p.slug, p.status
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
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (MenuItem, error) {
		var m MenuItem
		err := row.Scan(&m.ID, &m.Menu, &m.Sort, &m.Label, &m.PageID, &m.URL, &m.NewTab,
			&m.ParentID, &m.PageSlug, &m.PageStatus)
		return m, err
	})
}

// MenuItemInput is one entry supplied to ReplaceMenu.
type MenuItemInput struct {
	Label  string
	PageID *int64
	URL    string
	NewTab bool
}

// ReplaceMenu replaces a menu's items with the given ordered list,
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
	for i, item := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO cms_menu_items (menu, sort, label, page_id, url, new_tab)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			menu, i, item.Label, item.PageID, item.URL, item.NewTab); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
