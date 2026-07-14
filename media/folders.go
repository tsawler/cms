package media

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/tsawler/cms/internal/pgutil"
)

// Folder is a flat organizational bucket for media. Folders live entirely
// in Postgres; the object store's keys are untouched by foldering.
type Folder struct {
	ID    int64
	Name  string
	Count int // number of media items in the folder
}

var (
	// ErrDuplicateFolder is returned by CreateFolder for a taken name.
	ErrDuplicateFolder = errors.New("media: a folder with that name already exists")
	// ErrBadFolderName is returned for empty or over-long names.
	ErrBadFolderName = errors.New("media: folder names must be 1-60 characters")
)

// Folders returns all folders with their item counts, sorted by name.
func (m *Manager) Folders(ctx context.Context) ([]Folder, error) {
	rows, err := m.db.Query(ctx, `
		SELECT f.id, f.name, count(md.id)
		FROM cms_media_folders f
		LEFT JOIN cms_media md ON md.folder_id = f.id
		GROUP BY f.id, f.name
		ORDER BY f.name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Folder, error) {
		var f Folder
		err := row.Scan(&f.ID, &f.Name, &f.Count)
		return f, err
	})
}

// CreateFolder makes a new folder and returns it.
func (m *Manager) CreateFolder(ctx context.Context, name string) (*Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return nil, ErrBadFolderName
	}
	f := &Folder{Name: name}
	err := m.db.QueryRow(ctx,
		"INSERT INTO cms_media_folders (name) VALUES ($1) RETURNING id", name,
	).Scan(&f.ID)
	if pgutil.IsUniqueViolation(err) {
		return nil, ErrDuplicateFolder
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// DeleteFolder removes a folder; its contents become unfiled (the media
// rows' folder_id is nulled by the foreign key).
func (m *Manager) DeleteFolder(ctx context.Context, id int64) error {
	_, err := m.db.Exec(ctx, "DELETE FROM cms_media_folders WHERE id = $1", id)
	return err
}

// Move puts a media item into a folder, or back to unfiled when folderID is
// nil.
func (m *Manager) Move(ctx context.Context, mediaID int64, folderID *int64) error {
	tag, err := m.db.Exec(ctx,
		"UPDATE cms_media SET folder_id = $1 WHERE id = $2", folderID, mediaID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
