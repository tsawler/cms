package media

import (
	"context"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// seedLibrary fills a manager with one image in a folder (with alt text in
// two locales and a real uploader), one document, and one empty folder —
// enough that every field a manifest carries is exercised. It returns the
// uploader's email so a rebuilt database can re-seed the same person.
func seedLibrary(t *testing.T, ctx context.Context, db *sqldb.DB, m *Manager) string {
	t.Helper()

	const email = "uploader@example.com"
	userID, err := auth.NewStore(db).Insert(ctx, &auth.User{
		Email: email, Name: "Uploader", PasswordHash: "x", Role: auth.RoleEditor, Active: true})
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	album, err := m.CreateFolder(ctx, "Album", KindImage)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// An empty folder is the case only the folders manifest can preserve:
	// no item mentions it.
	if _, err := m.CreateFolder(ctx, "Empty", KindVideo); err != nil {
		t.Fatalf("CreateFolder(empty): %v", err)
	}

	img, err := m.Upload(ctx, "Sunset Photo.png", testImage(t, 40, 20, "png"), userID, &album.ID)
	if err != nil {
		t.Fatalf("Upload(image): %v", err)
	}
	if err := m.UpdateAlt(ctx, img.ID, "en", "The lighthouse at dusk"); err != nil {
		t.Fatalf("UpdateAlt(en): %v", err)
	}
	if err := m.UpdateAlt(ctx, img.ID, "fr", "Le phare au crépuscule"); err != nil {
		t.Fatalf("UpdateAlt(fr): %v", err)
	}
	if _, err := m.Upload(ctx, "Q3 Report (final).pdf", []byte("%PDF-1.4\n%stub\n"), 0, nil); err != nil {
		t.Fatalf("Upload(pdf): %v", err)
	}
	return email
}

// TestMediaRestoreRoundTrip is the whole point of manifests: a library
// uploaded into a bucket must come back intact into a database that knows
// nothing about it.
func TestMediaRestoreRoundTrip(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, objects := newTestManager(db)
		email := seedLibrary(t, ctx, db, m)

		before, err := m.All(ctx, "en", ListOptions{})
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		if len(before) != 2 {
			t.Fatalf("seeded %d items, want 2", len(before))
		}

		// Everything the database knew is gone; only the bucket remains.
		dbtest.Truncate(t, db)
		if n, err := m.Count(ctx); err != nil || n != 0 {
			t.Fatalf("Count after truncate = %d, %v; want 0", n, err)
		}
		// The same person exists in the rebuilt database, so attribution
		// should find them again by email.
		userID, err := auth.NewStore(db).Insert(ctx, &auth.User{
			Email: email, Name: "Uploader", PasswordHash: "x", Role: auth.RoleEditor, Active: true})
		if err != nil {
			t.Fatalf("re-seeding user: %v", err)
		}

		res, err := m.Restore(ctx, AdoptWhenEmpty)
		if err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if res.Adopted != 2 {
			t.Errorf("adopted %d items, want 2 (result %+v)", res.Adopted, res)
		}
		if res.Failed != 0 || res.Orphaned != 0 {
			t.Errorf("Restore reported failures: %+v", res)
		}

		after, err := m.All(ctx, "en", ListOptions{})
		if err != nil {
			t.Fatalf("All after restore: %v", err)
		}
		if len(after) != len(before) {
			t.Fatalf("restored %d items, want %d", len(after), len(before))
		}

		byKey := map[string]Media{}
		for _, md := range after {
			byKey[md.StoreKey] = md
		}
		for _, want := range before {
			got, ok := byKey[want.StoreKey]
			if !ok {
				t.Errorf("item %q (%s) did not come back", want.Filename, want.StoreKey)
				continue
			}
			if got.Filename != want.Filename {
				t.Errorf("filename = %q, want %q", got.Filename, want.Filename)
			}
			if got.Kind != want.Kind {
				t.Errorf("%s: kind = %q, want %q", want.Filename, got.Kind, want.Kind)
			}
			if got.Mime != want.Mime || got.Ext != want.Ext || got.VariantExt != want.VariantExt {
				t.Errorf("%s: mime/ext/variant = %q/%q/%q, want %q/%q/%q", want.Filename,
					got.Mime, got.Ext, got.VariantExt, want.Mime, want.Ext, want.VariantExt)
			}
			if got.Width != want.Width || got.Height != want.Height {
				t.Errorf("%s: dimensions = %dx%d, want %dx%d", want.Filename,
					got.Width, got.Height, want.Width, want.Height)
			}
			if got.Size != want.Size {
				t.Errorf("%s: size = %d, want %d", want.Filename, got.Size, want.Size)
			}
			if !got.CreatedAt.Equal(want.CreatedAt.UTC()) {
				t.Errorf("%s: created_at = %s, want %s", want.Filename, got.CreatedAt, want.CreatedAt.UTC())
			}
			// The URLs a page embeds must still point at objects that exist.
			for _, rendition := range []string{"original", "web", "thumb"} {
				if m.URL(&got, rendition) != m.URL(&want, rendition) {
					t.Errorf("%s: %s URL = %q, want %q", want.Filename, rendition,
						m.URL(&got, rendition), m.URL(&want, rendition))
				}
			}
		}

		// Alt text, per locale.
		var image Media
		for _, md := range after {
			if md.Kind == KindImage {
				image = md
			}
		}
		for locale, want := range map[string]string{"en": "The lighthouse at dusk", "fr": "Le phare au crépuscule"} {
			got, err := m.GetByID(ctx, image.ID, locale)
			if err != nil {
				t.Fatalf("GetByID(%s): %v", locale, err)
			}
			if got.Alt != want {
				t.Errorf("alt(%s) = %q, want %q", locale, got.Alt, want)
			}
		}

		// Attribution resolved through the email, not a stale row id.
		if image.UploadedBy == nil || *image.UploadedBy != userID {
			t.Errorf("uploaded_by = %v, want %d", image.UploadedBy, userID)
		}

		// Both folders are back — including the empty one, which no item
		// mentions — and the image is filed in the right one.
		folders, err := m.Folders(ctx)
		if err != nil {
			t.Fatalf("Folders: %v", err)
		}
		names := map[string]Folder{}
		for _, f := range folders {
			names[f.Name] = f
		}
		if len(folders) != 2 {
			t.Errorf("restored %d folders, want 2 (%v)", len(folders), names)
		}
		empty, ok := names["Empty"]
		if !ok {
			t.Error("the empty folder did not come back")
		} else if empty.Kind != KindVideo {
			t.Errorf("empty folder kind = %q, want %q", empty.Kind, KindVideo)
		}
		album, ok := names["Album"]
		if !ok {
			t.Fatal("the image folder did not come back")
		}
		if image.FolderID == nil || *image.FolderID != album.ID {
			t.Errorf("image folder = %v, want %d", image.FolderID, album.ID)
		}
		if album.Count != 1 {
			t.Errorf("album holds %d items, want 1", album.Count)
		}

		// Restoring did not write anything new to the bucket.
		if got := len(objects.keys(m.manifestRoot)); got != 3 {
			t.Errorf("bucket holds %d manifests, want 3 (2 items + folders)", got)
		}
	})
}

// TestMediaRestoreIsIdempotent covers the resumable case: a run interrupted
// halfway must not duplicate what it already adopted.
func TestMediaRestoreIsIdempotent(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)
		seedLibrary(t, ctx, db, m)
		dbtest.Truncate(t, db)

		if _, err := m.Restore(ctx, AdoptReconcile); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		res, err := m.Restore(ctx, AdoptReconcile)
		if err != nil {
			t.Fatalf("Restore(again): %v", err)
		}
		if res.Adopted != 0 {
			t.Errorf("second run adopted %d items, want 0", res.Adopted)
		}
		if res.Skipped != 2 {
			t.Errorf("second run skipped %d items, want 2", res.Skipped)
		}
		if res.DidWork() {
			t.Errorf("second run reported work: %+v", res)
		}
		if n, err := m.Count(ctx); err != nil || n != 2 {
			t.Errorf("Count = %d, %v; want 2 — the second run duplicated rows", n, err)
		}
	})
}

// TestMediaRestoreModes checks the guards that decide whether to look at the
// bucket at all.
func TestMediaRestoreModes(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)
		seedLibrary(t, ctx, db, m)
		dbtest.Truncate(t, db)

		// Off never reads the bucket.
		res, err := m.Restore(ctx, AdoptOff)
		if err != nil {
			t.Fatalf("Restore(off): %v", err)
		}
		if res.DidWork() {
			t.Errorf("AdoptOff did work: %+v", res)
		}

		// WhenEmpty adopts into the empty database...
		if _, err := m.Restore(ctx, AdoptWhenEmpty); err != nil {
			t.Fatalf("Restore(when empty): %v", err)
		}

		// ...and then stands down, without even listing.
		res, err = m.Restore(ctx, AdoptWhenEmpty)
		if err != nil {
			t.Fatalf("Restore(when empty, populated): %v", err)
		}
		if res.Skipped != 0 || res.DidWork() {
			t.Errorf("AdoptWhenEmpty ran against a populated database: %+v", res)
		}
	})
}

// TestMediaRestoreSkipsItemsWithoutObjects guards the case where a manifest
// outlives the binaries it describes: adopting it would put an unservable
// item in the library.
func TestMediaRestoreSkipsItemsWithoutObjects(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, objects := newTestManager(db)
		md, err := m.Upload(ctx, "vanishing.png", testImage(t, 10, 10, "png"), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		for _, key := range m.objectKeys(md) {
			if err := objects.Delete(ctx, key); err != nil {
				t.Fatalf("Delete object: %v", err)
			}
		}
		dbtest.Truncate(t, db)

		res, err := m.Restore(ctx, AdoptWhenEmpty)
		if err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if res.Adopted != 0 {
			t.Errorf("adopted %d items, want 0", res.Adopted)
		}
		if res.Orphaned != 1 {
			t.Errorf("orphaned = %d, want 1 (result %+v)", res.Orphaned, res)
		}
	})
}

// TestMediaManifestsTrackMutations checks that the sidecars stay current,
// since a manifest is only as good as the last write that touched it.
func TestMediaManifestsTrackMutations(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		md, err := m.Upload(ctx, "photo.png", testImage(t, 10, 10, "png"), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		manifestKey := m.manifestKey(md.ItemID())
		if _, err := m.readManifest(ctx, manifestKey); err != nil {
			t.Fatalf("Upload wrote no usable manifest: %v", err)
		}

		// Alt text and a move both change recoverable state.
		folder, err := m.CreateFolder(ctx, "Album", KindImage)
		if err != nil {
			t.Fatalf("CreateFolder: %v", err)
		}
		if err := m.UpdateAlt(ctx, md.ID, "en", "A red stripe"); err != nil {
			t.Fatalf("UpdateAlt: %v", err)
		}
		if err := m.Move(ctx, md.ID, &folder.ID); err != nil {
			t.Fatalf("Move: %v", err)
		}
		mf, err := m.readManifest(ctx, manifestKey)
		if err != nil {
			t.Fatalf("readManifest: %v", err)
		}
		if mf.Alt["en"] != "A red stripe" {
			t.Errorf("manifest alt = %q, want %q", mf.Alt["en"], "A red stripe")
		}
		if mf.Folder == nil || mf.Folder.Name != "Album" {
			t.Errorf("manifest folder = %v, want Album", mf.Folder)
		}

		// Deleting the folder unfiles the item, which its manifest must
		// reflect or a restore would refile it into a folder that is gone.
		if err := m.DeleteFolder(ctx, folder.ID); err != nil {
			t.Fatalf("DeleteFolder: %v", err)
		}
		mf, err = m.readManifest(ctx, manifestKey)
		if err != nil {
			t.Fatalf("readManifest after DeleteFolder: %v", err)
		}
		if mf.Folder != nil {
			t.Errorf("manifest still names folder %v after it was deleted", mf.Folder)
		}

		// Deleting the item takes its manifest with it, so a later restore
		// cannot resurrect it.
		if err := m.Delete(ctx, md.ID, "en"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := m.readManifest(ctx, manifestKey); err == nil {
			t.Error("Delete left the manifest behind; a restore would resurrect the item")
		}
		res, err := m.Restore(ctx, AdoptWhenEmpty)
		if err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if res.Adopted != 0 {
			t.Errorf("restore resurrected %d deleted items", res.Adopted)
		}
	})
}

// TestMediaSyncManifestsRepairsDrift covers the recovery path for manifest
// writes that failed while the object store was unreachable.
func TestMediaSyncManifestsRepairsDrift(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, objects := newTestManager(db)
		seedLibrary(t, ctx, db, m)

		// Wipe every manifest, as if none of the sidecar writes landed.
		for _, key := range objects.keys(m.manifestRoot) {
			if err := objects.Delete(ctx, key); err != nil {
				t.Fatalf("Delete manifest: %v", err)
			}
		}
		if got, err := m.Restore(ctx, AdoptReconcile); err != nil || got.DidWork() {
			t.Fatalf("Restore with no manifests = %+v, %v; want nothing to do", got, err)
		}

		n, err := m.SyncManifests(ctx)
		if err != nil {
			t.Fatalf("SyncManifests: %v", err)
		}
		if n != 2 {
			t.Errorf("SyncManifests wrote %d items, want 2", n)
		}

		// The bucket describes the library again: a fresh database rebuilds.
		dbtest.Truncate(t, db)
		res, err := m.Restore(ctx, AdoptWhenEmpty)
		if err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if res.Adopted != 2 {
			t.Errorf("adopted %d items after repair, want 2 (%+v)", res.Adopted, res)
		}
		if res.Folders != 2 {
			t.Errorf("restored %d folders after repair, want 2", res.Folders)
		}
	})
}

// TestMediaRestoreHonoursKeyPrefix checks that a deployment prefix scopes
// both the objects and the manifests, which is what keeps one site in a
// shared bucket from adopting another's library.
func TestMediaRestoreHonoursKeyPrefix(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		objects := newMemStore()
		logger := newTestLogger()

		acme := NewManager(db, prefixedMemStore{objects, "acme"}, logger)
		if _, err := acme.Upload(ctx, "theirs.png", testImage(t, 10, 10, "png"), 0, nil); err != nil {
			t.Fatalf("Upload(acme): %v", err)
		}
		for _, key := range objects.keys("") {
			if !strings.HasPrefix(key, "acme/") {
				t.Errorf("object %q escaped the deployment prefix", key)
			}
		}

		// A different deployment on the same bucket sees none of it.
		other := NewManager(db, prefixedMemStore{objects, "other"}, logger)
		res, err := other.Restore(ctx, AdoptWhenEmpty)
		if err != nil {
			t.Fatalf("Restore(other): %v", err)
		}
		if res.DidWork() || res.Adopted != 0 {
			t.Errorf("a differently-prefixed deployment adopted %+v", res)
		}

		// The owning deployment gets its library back.
		dbtest.Truncate(t, db)
		res, err = acme.Restore(ctx, AdoptWhenEmpty)
		if err != nil {
			t.Fatalf("Restore(acme): %v", err)
		}
		if res.Adopted != 1 {
			t.Errorf("adopted %d items, want 1 (%+v)", res.Adopted, res)
		}
	})
}

// prefixedMemStore is a memStore that reports a deployment key prefix.
type prefixedMemStore struct {
	*memStore
	prefix string
}

func (p prefixedMemStore) KeyPrefix() string { return p.prefix }
