package media

// Rebuilding the media library from the bucket. Manifests (manifest.go) make
// every item in a bucket self-describing, so a deployment pointed at a bucket
// that already holds media can adopt it instead of starting empty: the case
// that matters is a database restored from scratch, or a second environment
// pointed at a copy of production's bucket.
//
// Adoption is additive. It inserts rows for manifests the database does not
// have and never deletes anything, because the failure modes of listing a
// bucket — a truncated page, a transient error, credentials scoped to the
// wrong prefix — all look like "objects are missing", and acting on that
// would destroy the library it was meant to protect.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// AdoptMode controls whether Restore rebuilds the media library from the
// object store.
type AdoptMode int

const (
	// AdoptWhenEmpty adopts the bucket's media only when the database has
	// none — the default, and the case of a fresh deployment pointed at a
	// bucket that already holds content. Once any media exists it never
	// runs again, so ordinary startups do no bucket work beyond one list.
	AdoptWhenEmpty AdoptMode = iota
	// AdoptOff never touches the bucket.
	AdoptOff
	// AdoptReconcile checks on every startup and adopts anything in the
	// bucket the database is missing. It covers what AdoptWhenEmpty
	// cannot — restoring a database backup older than the bucket — at the
	// cost of listing the manifests each time.
	AdoptReconcile
)

// restoreLockName serializes adoption across instances starting at once. It
// is distinct from the migration lock so a long adoption does not hold up
// another instance's schema check.
const restoreLockName = "cms_media_restore"

// manifestFetchers bounds how many manifests are fetched at once. They are
// small and latency-bound, so concurrency helps a lot and a modest cap keeps
// a large library from opening hundreds of connections.
const manifestFetchers = 8

// RestoreResult reports what an adoption run did.
type RestoreResult struct {
	// Adopted is how many media items were inserted.
	Adopted int
	// Folders is how many folders were created.
	Folders int
	// Skipped counts manifests already present in the database.
	Skipped int
	// Orphaned counts manifests whose objects are missing from the bucket;
	// they are not adopted, since the item could not be served.
	Orphaned int
	// Failed counts manifests that could not be read or inserted. They are
	// logged individually; a re-run retries them.
	Failed int
}

// DidWork reports whether the run found anything to act on, for callers
// deciding whether it is worth logging. Skipped alone does not count: a
// reconcile that confirms the database already matches the bucket is the
// quiet, expected case.
func (r RestoreResult) DidWork() bool {
	return r.Adopted+r.Folders+r.Orphaned+r.Failed > 0
}

// Restore rebuilds media rows from the manifests in the object store,
// according to mode. It returns what it did.
//
// It is safe to call on every startup and safe to call from several
// instances at once: an advisory lock serializes the run, and items are
// keyed by store_key, so an interrupted run resumes rather than duplicating.
// Individual bad manifests are logged and counted, not fatal — one unreadable
// sidecar must not stop the other thousand items from coming back.
//
// Adoption needs an ObjectStore that implements Lister. With any other store
// it reports nothing done rather than failing, since a host that supplied its
// own store can rebuild by whatever means suits it.
func (m *Manager) Restore(ctx context.Context, mode AdoptMode) (RestoreResult, error) {
	var res RestoreResult
	if mode == AdoptOff {
		return res, nil
	}
	lister, ok := m.objects.(Lister)
	if !ok {
		m.logger.Debug("cms media: object store cannot list, skipping adoption")
		return res, nil
	}

	// A dedicated connection: the advisory lock is session-scoped and has
	// to be taken and released on one connection to serialize anything.
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return res, fmt.Errorf("cms media: acquiring connection: %w", err)
	}
	defer conn.Close()
	unlock, err := m.db.Dialect().Lock(ctx, conn.Execer(), restoreLockName)
	if err != nil {
		return res, fmt.Errorf("cms media: %w", err)
	}
	defer unlock()

	// Re-checked under the lock, so the instance that lost the race sees
	// the winner's work rather than repeating it.
	if mode == AdoptWhenEmpty {
		n, err := m.Count(ctx)
		if err != nil {
			return res, err
		}
		if n > 0 {
			return res, nil
		}
	}

	entries, err := lister.List(ctx, m.manifestRoot)
	if err != nil {
		return res, err
	}
	if len(entries) == 0 {
		return res, nil
	}

	// One flat listing of the media root gives every object's key, which is
	// what proves a manifest's item is actually servable before its row is
	// written.
	objects, err := lister.List(ctx, m.keyRoot)
	if err != nil {
		return res, err
	}
	present := make(map[string]bool, len(objects))
	for _, o := range objects {
		present[o.Key] = true
	}

	foldersKey := m.manifestRoot + foldersManifestName
	var itemKeys []string
	for _, e := range entries {
		if e.Key == foldersKey || !strings.HasSuffix(e.Key, ".json") {
			continue
		}
		itemKeys = append(itemKeys, e.Key)
	}

	folderIDs, created, err := m.restoreFolders(ctx, foldersKey)
	if err != nil {
		return res, err
	}
	res.Folders = created

	for _, mf := range m.fetchManifests(ctx, itemKeys, &res) {
		if err := m.adopt(ctx, mf, folderIDs, present, &res); err != nil {
			res.Failed++
			m.logger.Error("cms media: adopting item", "item", mf.ItemID, "err", err)
		}
	}

	// Worth saying only when something was actually taken on: a
	// single-tenant bucket needs no prefix, so warning about its absence on
	// every startup would be noise. Adopting from an unprefixed bucket is
	// the moment it could turn out to be someone else's media.
	if res.Adopted > 0 && m.keyRoot == keyRoot("") {
		m.logger.Warn("cms media: adopted from a bucket with no key prefix; "+
			"set S3_KEY_PREFIX if this bucket is shared with other sites",
			"items", res.Adopted)
	}
	return res, nil
}

// fetchManifests reads the item manifests concurrently, counting the ones
// that could not be read. Only the fetch is parallel; the inserts that follow
// run on one goroutine, which keeps folder creation free of races without a
// second lock.
func (m *Manager) fetchManifests(ctx context.Context, keys []string, res *RestoreResult) []*manifest {
	var (
		mu  sync.Mutex
		out = make([]*manifest, len(keys))
		wg  sync.WaitGroup
	)
	sem := make(chan struct{}, manifestFetchers)
	for i, key := range keys {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			mf, err := m.readManifest(ctx, key)
			if err != nil {
				mu.Lock()
				res.Failed++
				mu.Unlock()
				m.logger.Error("cms media: reading manifest", "key", key, "err", err)
				return
			}
			out[i] = mf
		})
	}
	wg.Wait()

	kept := out[:0]
	for _, mf := range out {
		if mf != nil {
			kept = append(kept, mf)
		}
	}
	return kept
}

// folderKey is the (kind, name) pair the database makes unique, as a map key.
func folderKey(kind Kind, name string) string { return string(kind) + "\x00" + name }

// restoreFolders creates the folders named by the folders manifest and
// returns the id of every folder that now exists, keyed by kind and name. A
// missing folders manifest is not an error: items still name their folders,
// and adopt creates those on demand.
func (m *Manager) restoreFolders(ctx context.Context, key string) (map[string]int64, int, error) {
	ids := map[string]int64{}
	existing, err := m.Folders(ctx)
	if err != nil {
		return nil, 0, err
	}
	for _, f := range existing {
		ids[folderKey(f.Kind, f.Name)] = f.ID
	}

	fm, err := m.readFoldersManifest(ctx, key)
	if errors.Is(err, ErrObjectNotFound) {
		return ids, 0, nil
	}
	if err != nil {
		// A bucket whose folder list is unreadable can still have its items
		// adopted, with folders rebuilt from what the items name. Only
		// folders holding nothing are lost.
		m.logger.Warn("cms media: reading folders manifest", "key", key, "err", err)
		return ids, 0, nil
	}

	var created int
	for _, f := range fm.Folders {
		if _, ok := ids[folderKey(f.Kind, f.Name)]; ok {
			continue
		}
		id, err := m.ensureFolder(ctx, f)
		if err != nil {
			m.logger.Warn("cms media: creating folder during adoption", "folder", f.Name, "err", err)
			continue
		}
		ids[folderKey(f.Kind, f.Name)] = id
		created++
	}
	return ids, created, nil
}

// ensureFolder returns the id of the named folder, creating it if needed.
func (m *Manager) ensureFolder(ctx context.Context, f manifestFolder) (int64, error) {
	folder, err := m.CreateFolder(ctx, f.Name, f.Kind)
	if err == nil {
		return folder.ID, nil
	}
	if !errors.Is(err, ErrDuplicateFolder) {
		return 0, err
	}
	// Raced, or already there under a name differing only in case.
	var id int64
	err = m.db.QueryRow(ctx,
		"SELECT id FROM cms_media_folders WHERE kind = $1 AND name = $2", f.Kind, f.Name).Scan(&id)
	return id, err
}

// adopt inserts one manifest's rows, unless the item is already in the
// database or its objects are gone.
func (m *Manager) adopt(ctx context.Context, mf *manifest, folderIDs map[string]int64, present map[string]bool, res *RestoreResult) error {
	var existing int64
	switch err := m.db.QueryRow(ctx,
		"SELECT id FROM cms_media WHERE store_key = $1", mf.StoreKey).Scan(&existing); {
	case err == nil:
		res.Skipped++
		return nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return err
	}

	md := mf.media()
	keys := m.objectKeys(md)
	// keys[0] is the item itself; the rest are derived renditions. Without
	// the item there is nothing to serve, so the row would be a dead entry
	// in the library.
	if !present[keys[0]] {
		res.Orphaned++
		m.logger.Warn("cms media: manifest has no object, not adopting",
			"item", mf.ItemID, "key", keys[0])
		return nil
	}
	for _, key := range keys[1:] {
		if !present[key] {
			m.logger.Warn("cms media: adopted item is missing a rendition",
				"item", mf.ItemID, "key", key)
		}
	}

	if mf.Folder != nil {
		id, ok := folderIDs[folderKey(mf.Folder.Kind, mf.Folder.Name)]
		if !ok {
			newID, err := m.ensureFolder(ctx, *mf.Folder)
			if err != nil {
				return err
			}
			folderIDs[folderKey(mf.Folder.Kind, mf.Folder.Name)] = newID
			res.Folders++
			id = newID
		}
		md.FolderID = &id
	}

	if mf.UploadedBy != "" {
		var userID int64
		switch err := m.db.QueryRow(ctx,
			"SELECT id FROM cms_users WHERE email = $1", mf.UploadedBy).Scan(&userID); {
		case err == nil:
			md.UploadedBy = &userID
		case errors.Is(err, sql.ErrNoRows):
			// The uploader has no account in this database. The item is
			// still worth having; it just loses its attribution.
		default:
			return err
		}
	}

	id, err := m.db.InsertID(ctx, `
		INSERT INTO cms_media (kind, store_key, filename, mime, ext, variant_ext, width, height, size, folder_id, uploaded_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		md.Kind, md.StoreKey, md.Filename, md.Mime, md.Ext, md.VariantExt,
		md.Width, md.Height, md.Size, md.FolderID, md.UploadedBy, md.CreatedAt)
	if err != nil {
		return err
	}

	for locale, alt := range mf.Alt {
		if _, err := m.db.Exec(ctx, `
			INSERT INTO cms_media_meta (media_id, locale, alt_text)
			VALUES ($1, $2, $3)
			ON CONFLICT (media_id, locale) DO UPDATE SET alt_text = EXCLUDED.alt_text`,
			id, locale, alt); err != nil {
			return err
		}
	}

	res.Adopted++
	return nil
}

// media converts a manifest to the record it describes, less the ids that
// only the database can assign.
func (mf *manifest) media() *Media {
	createdAt := mf.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return &Media{
		Kind:       mf.Kind,
		StoreKey:   mf.StoreKey,
		Filename:   mf.Filename,
		Mime:       mf.Mime,
		Ext:        mf.Ext,
		VariantExt: mf.VariantExt,
		Width:      mf.Width,
		Height:     mf.Height,
		Size:       mf.Size,
		CreatedAt:  createdAt,
	}
}
