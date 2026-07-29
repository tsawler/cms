package media

// Manifests make the bucket self-describing. Every media item gets a small
// JSON sidecar at "<manifestRoot><item id>.json" carrying everything the
// database knows about it that the object keys do not: the original
// filename, the alt text for each locale, which folder it sits in, its
// dimensions, and who uploaded it. Folders get one manifest of their own, so
// even empty ones survive.
//
// The point is recovery. With sidecars, rebuilding cms_media from a bucket
// is a replay of recorded facts rather than guesswork over key names — see
// Manager.Restore. The cost is that every write which changes an item's
// metadata has to rewrite its manifest; the mutation points are Upload,
// UpdateAlt, Move, Delete, CreateFolder, and DeleteFolder, and
// SyncManifests repairs whatever drifted when one of those writes failed.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tsawler/cms/internal/sqldb"
)

// manifestVersion is the sidecar format version. Restore refuses manifests
// from a future version rather than misreading them.
const manifestVersion = 1

// foldersManifestName is the one manifest that describes folders rather than
// an item. The leading underscore keeps it out of the item id namespace,
// which is always hex.
const foldersManifestName = "_folders.json"

// manifest is the recoverable state of one media item.
type manifest struct {
	Version int    `json:"v"`
	ItemID  string `json:"id"`
	Kind    Kind   `json:"kind"`
	// StoreKey is the media-root-relative key, exactly as stored in
	// cms_media.store_key — never an absolute bucket key, so a manifest
	// stays valid if the bucket's KeyPrefix changes.
	StoreKey   string            `json:"store_key"`
	Filename   string            `json:"filename"`
	Mime       string            `json:"mime"`
	Ext        string            `json:"ext"`
	VariantExt string            `json:"variant_ext,omitempty"`
	Width      int               `json:"width,omitempty"`
	Height     int               `json:"height,omitempty"`
	Size       int64             `json:"size"`
	Folder     *manifestFolder   `json:"folder,omitempty"`
	UploadedBy string            `json:"uploaded_by,omitempty"`
	Alt        map[string]string `json:"alt,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

// manifestFolder identifies a folder by the pair that is unique in the
// database, rather than by an id that would mean nothing in a rebuilt one.
type manifestFolder struct {
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
}

// foldersManifest lists every folder, so folders that hold nothing are not
// lost on restore — no item manifest would mention them.
type foldersManifest struct {
	Version int              `json:"v"`
	Folders []manifestFolder `json:"folders"`
}

// manifestKey returns the absolute bucket key of one item's manifest.
func (m *Manager) manifestKey(itemID string) string {
	return m.manifestRoot + itemID + ".json"
}

// buildManifest assembles an item's manifest from the database. It reads
// live rather than taking a caller's copy so that a manifest written after
// any mutation reflects committed state.
func (m *Manager) buildManifest(ctx context.Context, id int64) (*manifest, error) {
	// Locale "" — the alt text for every locale is collected separately
	// below, so the joined column is not wanted here.
	md, err := m.GetByID(ctx, id, "")
	if err != nil {
		return nil, err
	}

	mf := &manifest{
		Version:    manifestVersion,
		ItemID:     md.ItemID(),
		Kind:       md.Kind,
		StoreKey:   md.StoreKey,
		Filename:   md.Filename,
		Mime:       md.Mime,
		Ext:        md.Ext,
		VariantExt: md.VariantExt,
		Width:      md.Width,
		Height:     md.Height,
		Size:       md.Size,
		CreatedAt:  md.CreatedAt.UTC(),
	}

	rows, err := m.db.Query(ctx,
		"SELECT locale, alt_text FROM cms_media_meta WHERE media_id = $1", id)
	if err != nil {
		return nil, err
	}
	type localeAlt struct{ locale, alt string }
	alts, err := sqldb.CollectRows(rows, func(row sqldb.Scanner) (localeAlt, error) {
		var la localeAlt
		err := row.Scan(&la.locale, &la.alt)
		return la, err
	})
	if err != nil {
		return nil, err
	}
	for _, la := range alts {
		if la.alt == "" {
			continue
		}
		if mf.Alt == nil {
			mf.Alt = map[string]string{}
		}
		mf.Alt[la.locale] = la.alt
	}

	if md.FolderID != nil {
		var f manifestFolder
		err := m.db.QueryRow(ctx,
			"SELECT kind, name FROM cms_media_folders WHERE id = $1", *md.FolderID).Scan(&f.Kind, &f.Name)
		if err != nil {
			return nil, err
		}
		mf.Folder = &f
	}

	if md.UploadedBy != nil {
		// The email, not the id: user ids are meaningless in a database
		// rebuilt from this bucket, but emails identify the same person.
		// A user deleted since the upload simply leaves it blank.
		var email string
		switch err := m.db.QueryRow(ctx,
			"SELECT email FROM cms_users WHERE id = $1", *md.UploadedBy).Scan(&email); {
		case err == nil:
			mf.UploadedBy = email
		case errors.Is(err, sql.ErrNoRows):
		default:
			return nil, err
		}
	}

	return mf, nil
}

// writeManifest builds and stores one item's manifest.
func (m *Manager) writeManifest(ctx context.Context, id int64) error {
	mf, err := m.buildManifest(ctx, id)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return m.objects.Put(ctx, m.manifestKey(mf.ItemID), "application/json", bytes.NewReader(data))
}

// syncManifest writes a manifest without letting a failure fail the caller's
// operation. A missing or stale manifest costs nothing until someone
// restores from this bucket, and SyncManifests repairs it; refusing an
// editor's alt-text edit because the bucket hiccuped would be worse. The
// error is logged loudly because it is silent data loss otherwise.
func (m *Manager) syncManifest(ctx context.Context, id int64) {
	if err := m.writeManifest(ctx, id); err != nil {
		m.logger.Error("cms media: writing manifest; this item will not be recoverable from the bucket until SyncManifests runs",
			"media_id", id, "err", err)
	}
}

// deleteManifest removes one item's manifest.
func (m *Manager) deleteManifest(ctx context.Context, itemID string) error {
	return m.objects.Delete(ctx, m.manifestKey(itemID))
}

// writeFoldersManifest stores the folder list.
func (m *Manager) writeFoldersManifest(ctx context.Context) error {
	folders, err := m.Folders(ctx)
	if err != nil {
		return err
	}
	fm := foldersManifest{Version: manifestVersion, Folders: make([]manifestFolder, len(folders))}
	for i, f := range folders {
		fm.Folders[i] = manifestFolder{Kind: f.Kind, Name: f.Name}
	}
	data, err := json.MarshalIndent(fm, "", "  ")
	if err != nil {
		return err
	}
	return m.objects.Put(ctx, m.manifestRoot+foldersManifestName, "application/json", bytes.NewReader(data))
}

// syncFoldersManifest writes the folder list, logging rather than failing.
func (m *Manager) syncFoldersManifest(ctx context.Context) {
	if err := m.writeFoldersManifest(ctx); err != nil {
		m.logger.Error("cms media: writing folders manifest", "err", err)
	}
}

// readManifest fetches and validates one item manifest.
func (m *Manager) readManifest(ctx context.Context, key string) (*manifest, error) {
	data, err := m.getObject(ctx, key)
	if err != nil {
		return nil, err
	}
	var mf manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("media: parsing manifest %s: %w", key, err)
	}
	if mf.Version > manifestVersion {
		return nil, fmt.Errorf("media: manifest %s is version %d, this build understands %d",
			key, mf.Version, manifestVersion)
	}
	if mf.StoreKey == "" || mf.ItemID == "" {
		return nil, fmt.Errorf("media: manifest %s has no store key", key)
	}
	switch mf.Kind {
	case KindImage, KindFile, KindVideo:
	default:
		return nil, fmt.Errorf("media: manifest %s has unknown kind %q", key, mf.Kind)
	}
	return &mf, nil
}

// readFoldersManifest fetches and validates the folder list.
func (m *Manager) readFoldersManifest(ctx context.Context, key string) (*foldersManifest, error) {
	data, err := m.getObject(ctx, key)
	if err != nil {
		return nil, err
	}
	var fm foldersManifest
	if err := json.Unmarshal(data, &fm); err != nil {
		return nil, fmt.Errorf("media: parsing manifest %s: %w", key, err)
	}
	if fm.Version > manifestVersion {
		return nil, fmt.Errorf("media: manifest %s is version %d, this build understands %d",
			key, fm.Version, manifestVersion)
	}
	return &fm, nil
}

// getObject reads one whole object.
func (m *Manager) getObject(ctx context.Context, key string) ([]byte, error) {
	body, _, err := m.objects.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

// SyncManifests rewrites every manifest from the database, and returns how
// many items it wrote. It is the repair for drift: manifest writes during
// normal operation are best-effort, so an object store that was briefly
// unreachable leaves items whose sidecar is missing or stale. Running this
// makes the bucket a faithful description of the library again.
//
// It is safe to run at any time and costs one small PUT per item.
func (m *Manager) SyncManifests(ctx context.Context) (int, error) {
	if err := m.writeFoldersManifest(ctx); err != nil {
		return 0, err
	}
	items, err := m.All(ctx, "", ListOptions{})
	if err != nil {
		return 0, err
	}
	for i, md := range items {
		if err := m.writeManifest(ctx, md.ID); err != nil {
			return i, err
		}
	}
	return len(items), nil
}
