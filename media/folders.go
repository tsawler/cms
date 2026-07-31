package media

import (
	"context"
	"errors"
	"strings"

	"github.com/tsawler/cms/internal/dberr"
	"github.com/tsawler/cms/internal/sqldb"
)

// Folder is a flat organizational bucket for media, scoped to one media
// kind — a folder made among the images exists only there. Folders live
// entirely in Postgres; the object store's keys are untouched by
// foldering.
type Folder struct {
	ID    int64
	Kind  Kind
	Name  string
	Count int // number of media items in the folder
}

var (
	// ErrDuplicateFolder is returned by CreateFolder for a name already
	// used within the same kind.
	ErrDuplicateFolder = errors.New("media: a folder with that name already exists")
	// ErrBadFolderName is returned for empty or over-long names.
	ErrBadFolderName = errors.New("media: folder names must be 1-60 characters")
	// ErrBadFolderKind is returned for a kind that isn't image, file, or
	// video.
	ErrBadFolderKind = errors.New("media: unknown folder kind")
	// ErrFolderNotEmpty is returned by DeleteFolder for a folder that
	// still holds media.
	ErrFolderNotEmpty = errors.New("media: the folder still has files in it")
)

// Folders returns all folders — every kind — with their item counts,
// sorted by name. Callers filter to the kind they present.
func (m *Manager) Folders(ctx context.Context) ([]Folder, error) {
	rows, err := m.db.Query(ctx, `
		SELECT f.id, f.kind, f.name, count(md.id)
		FROM cms_media_folders f
		LEFT JOIN cms_media md ON md.folder_id = f.id
		GROUP BY f.id, f.kind, f.name
		ORDER BY f.name`)
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (Folder, error) {
		var f Folder
		err := row.Scan(&f.ID, &f.Kind, &f.Name, &f.Count)
		return f, err
	})
}

// CreateFolder makes a new folder of the given kind and returns it.
func (m *Manager) CreateFolder(ctx context.Context, name string, kind Kind) (*Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return nil, ErrBadFolderName
	}
	switch kind {
	case KindImage, KindFile, KindVideo:
	default:
		return nil, ErrBadFolderKind
	}
	f := &Folder{Name: name, Kind: kind}
	id, err := m.db.InsertID(ctx,
		"INSERT INTO cms_media_folders (name, kind) VALUES ($1, $2)", name, kind)
	if dberr.IsUniqueViolation(err) {
		return nil, ErrDuplicateFolder
	}
	if err != nil {
		return nil, err
	}
	f.ID = id
	// Folders live only in the database, so an empty one would vanish on a
	// restore unless the bucket records it.
	m.syncFoldersManifest(ctx)
	return f, nil
}

// DeleteFolder removes a folder, but only an empty one — a folder still
// holding media comes back ErrFolderNotEmpty and is left alone. Deleting
// it used to unfile its contents, which scattered in one click the files
// an editor had just gathered; emptying it first says the same thing
// deliberately, and the files stay where they can be seen while you do.
//
// Deleting a folder that is already gone is not an error: two tabs open
// on the same folder can both submit the delete.
func (m *Manager) DeleteFolder(ctx context.Context, id int64) error {
	// One statement, so a file moved in concurrently can't be orphaned by
	// a delete that checked the folder was empty a moment earlier.
	tag, err := m.db.Exec(ctx, `
		DELETE FROM cms_media_folders
		WHERE id = $1
		  AND NOT EXISTS (SELECT 1 FROM cms_media WHERE folder_id = $1)`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Nothing was deleted: either the folder holds media, or it was
		// never there. Only the first is worth reporting.
		var n int
		if err := m.db.QueryRow(ctx,
			"SELECT count(*) FROM cms_media WHERE folder_id = $1", id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrFolderNotEmpty
		}
		return nil
	}
	m.syncFoldersManifest(ctx)
	return nil
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
	m.syncManifest(ctx, mediaID)
	return nil
}
