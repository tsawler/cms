package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
)

// memStore is an in-memory ObjectStore, so media tests exercise the real
// upload path (which writes several objects per image) without S3.
type memStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemStore() *memStore { return &memStore{objects: map[string][]byte{}} }

func (s *memStore) Put(_ context.Context, key, _ string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	return nil
}

func (s *memStore) Get(_ context.Context, key string) (io.ReadCloser, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, "", ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), "application/octet-stream", nil
}

func (s *memStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *memStore) PublicURL(key string) string { return "/" + key }

// List makes memStore a Lister, so restore tests exercise the real adoption
// path.
func (s *memStore) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ObjectInfo
	for key, data := range s.objects {
		if strings.HasPrefix(key, prefix) {
			out = append(out, ObjectInfo{Key: key, Size: int64(len(data))})
		}
	}
	return out, nil
}

// keys returns every stored key with the given prefix, for assertions.
func (s *memStore) keys(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	return out
}

func (s *memStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

// newTestLogger returns a logger that discards, since these tests assert on
// state rather than output.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestManager returns a Manager backed by db and a fresh in-memory object
// store, plus the store so tests can assert on what was written.
func newTestManager(db *sqldb.DB) (*Manager, *memStore) {
	objects := newMemStore()
	return NewManager(db, objects, newTestLogger()), objects
}

func TestMediaUploadAndGet(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, objects := newTestManager(db)

		png := testImage(t, 40, 20, "png")
		md, err := m.Upload(ctx, "photo.png", png, 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if md.ID == 0 {
			t.Fatal("Upload did not set the media id")
		}
		if md.CreatedAt.IsZero() {
			t.Error("Upload did not set created_at")
		}
		if md.Kind != KindImage {
			t.Errorf("kind = %q, want %q", md.Kind, KindImage)
		}
		if md.Width != 40 || md.Height != 20 {
			t.Errorf("dimensions = %dx%d, want 40x20", md.Width, md.Height)
		}
		if objects.len() == 0 {
			t.Error("Upload wrote no objects to the store")
		}

		got, err := m.GetByID(ctx, md.ID, "en")
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Filename != "photo.png" {
			t.Errorf("filename = %q, want %q", got.Filename, "photo.png")
		}
		if got.StoreKey != md.StoreKey {
			t.Errorf("store_key = %q, want %q", got.StoreKey, md.StoreKey)
		}
		// Store keys are relative to the media root: nothing in the
		// database embeds "media/" or the deployment prefix.
		if strings.HasPrefix(got.StoreKey, "media/") {
			t.Errorf("store_key = %q, want a key relative to the media root", got.StoreKey)
		}
		if got.Size != int64(len(png)) {
			t.Errorf("size = %d, want %d", got.Size, len(png))
		}
		// An unfiled upload has no folder.
		if got.FolderID != nil {
			t.Errorf("folder_id = %v, want nil", got.FolderID)
		}
		// uploadedBy 0 is documented to mean "nobody".
		if got.UploadedBy != nil {
			t.Errorf("uploaded_by = %v, want nil", got.UploadedBy)
		}
	})
}

func TestMediaGetNotFound(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		if _, err := m.GetByID(ctx, 4242, "en"); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetByID(missing) = %v, want ErrNotFound", err)
		}
		if err := m.Move(ctx, 4242, nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("Move(missing) = %v, want ErrNotFound", err)
		}
	})
}

func TestMediaAltTextPerLocale(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)
		md, err := m.Upload(ctx, "photo.png", testImage(t, 10, 10, "png"), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}

		if err := m.UpdateAlt(ctx, md.ID, "en", "A red stripe"); err != nil {
			t.Fatalf("UpdateAlt(en): %v", err)
		}
		if err := m.UpdateAlt(ctx, md.ID, "fr", "Une bande rouge"); err != nil {
			t.Fatalf("UpdateAlt(fr): %v", err)
		}

		for locale, want := range map[string]string{"en": "A red stripe", "fr": "Une bande rouge"} {
			got, err := m.GetByID(ctx, md.ID, locale)
			if err != nil {
				t.Fatalf("GetByID(%s): %v", locale, err)
			}
			if got.Alt != want {
				t.Errorf("alt(%s) = %q, want %q", locale, got.Alt, want)
			}
		}
		// A locale with no alt text reads as empty, not as an error.
		got, err := m.GetByID(ctx, md.ID, "de")
		if err != nil {
			t.Fatalf("GetByID(de): %v", err)
		}
		if got.Alt != "" {
			t.Errorf("alt(de) = %q, want empty", got.Alt)
		}

		// Setting it again replaces rather than duplicating — the upsert.
		if err := m.UpdateAlt(ctx, md.ID, "en", "Updated"); err != nil {
			t.Fatalf("UpdateAlt(replace): %v", err)
		}
		got, err = m.GetByID(ctx, md.ID, "en")
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Alt != "Updated" {
			t.Errorf("alt after replace = %q, want %q", got.Alt, "Updated")
		}
	})
}

func TestMediaAllFiltering(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		img, err := m.Upload(ctx, "Sunset Photo.png", testImage(t, 10, 10, "png"), 0, nil)
		if err != nil {
			t.Fatalf("Upload(image): %v", err)
		}
		doc, err := m.Upload(ctx, "report.pdf", []byte("%PDF-1.4\n%stub\n"), 0, nil)
		if err != nil {
			t.Fatalf("Upload(pdf): %v", err)
		}

		namesOf := func(items []Media) []string {
			out := make([]string, len(items))
			for i, it := range items {
				out[i] = it.Filename
			}
			return out
		}

		all, err := m.All(ctx, "en", ListOptions{})
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("All = %v, want both items", namesOf(all))
		}

		// Filter by kind.
		images, err := m.All(ctx, "en", ListOptions{Kind: KindImage})
		if err != nil {
			t.Fatalf("All(images): %v", err)
		}
		if len(images) != 1 || images[0].ID != img.ID {
			t.Errorf("All(images) = %v, want just the image", namesOf(images))
		}
		files, err := m.All(ctx, "en", ListOptions{Kind: KindFile})
		if err != nil {
			t.Fatalf("All(files): %v", err)
		}
		if len(files) != 1 || files[0].ID != doc.ID {
			t.Errorf("All(files) = %v, want just the pdf", namesOf(files))
		}

		// Query is documented as a case-insensitive substring match.
		for _, probe := range []string{"sunset", "SUNSET", "set Pho"} {
			got, err := m.All(ctx, "en", ListOptions{Query: probe})
			if err != nil {
				t.Fatalf("All(query %q): %v", probe, err)
			}
			if len(got) != 1 || got[0].ID != img.ID {
				t.Errorf("All(query %q) = %v, want just the image", probe, namesOf(got))
			}
		}
		none, err := m.All(ctx, "en", ListOptions{Query: "nothing-matches"})
		if err != nil {
			t.Fatalf("All(no match): %v", err)
		}
		if len(none) != 0 {
			t.Errorf("All(no match) = %v, want none", namesOf(none))
		}

		n, err := m.Count(ctx)
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n != 2 {
			t.Errorf("Count = %d, want 2", n)
		}
	})
}

func TestMediaQueryEscapesWildcards(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		if _, err := m.Upload(ctx, "a_b.png", testImage(t, 10, 10, "png"), 0, nil); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if _, err := m.Upload(ctx, "axb.png", testImage(t, 10, 10, "png"), 0, nil); err != nil {
			t.Fatalf("Upload: %v", err)
		}

		// "_" is a LIKE wildcard; the escaper must make it literal, so this
		// matches only the file that really contains an underscore.
		got, err := m.All(ctx, "en", ListOptions{Query: "a_b"})
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		if len(got) != 1 || got[0].Filename != "a_b.png" {
			var names []string
			for _, g := range got {
				names = append(names, g.Filename)
			}
			t.Errorf("All(query \"a_b\") = %v, want just a_b.png", names)
		}
	})
}

func TestMediaDelete(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, objects := newTestManager(db)
		md, err := m.Upload(ctx, "doomed.png", testImage(t, 10, 10, "png"), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if objects.len() == 0 {
			t.Fatal("Upload wrote no objects")
		}

		if err := m.Delete(ctx, md.ID, "en"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := m.GetByID(ctx, md.ID, "en"); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetByID after Delete = %v, want ErrNotFound", err)
		}
		// The bucket objects go with the row.
		if objects.len() != 0 {
			t.Errorf("Delete left %d objects behind, want 0", objects.len())
		}
		if err := m.Delete(ctx, md.ID, "en"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Delete(already gone) = %v, want ErrNotFound", err)
		}
	})
}

func TestMediaFolders(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		photos, err := m.CreateFolder(ctx, "Photos", KindImage)
		if err != nil {
			t.Fatalf("CreateFolder: %v", err)
		}
		if photos.ID == 0 {
			t.Fatal("CreateFolder did not set the folder id")
		}
		docs, err := m.CreateFolder(ctx, "Documents", KindFile)
		if err != nil {
			t.Fatalf("CreateFolder(files): %v", err)
		}

		// Names are unique only within a kind, so the same name in another
		// kind is allowed...
		if _, err := m.CreateFolder(ctx, "Photos", KindVideo); err != nil {
			t.Errorf("CreateFolder(same name, other kind) = %v, want success", err)
		}
		// ...but a repeat within one kind is not.
		if _, err := m.CreateFolder(ctx, "Photos", KindImage); !errors.Is(err, ErrDuplicateFolder) {
			t.Errorf("CreateFolder(duplicate) = %v, want ErrDuplicateFolder", err)
		}

		// Validation.
		if _, err := m.CreateFolder(ctx, "   ", KindImage); !errors.Is(err, ErrBadFolderName) {
			t.Errorf("CreateFolder(blank) = %v, want ErrBadFolderName", err)
		}
		if _, err := m.CreateFolder(ctx, "ok", Kind("nonsense")); !errors.Is(err, ErrBadFolderKind) {
			t.Errorf("CreateFolder(bad kind) = %v, want ErrBadFolderKind", err)
		}

		// Counts come from a LEFT JOIN, so an empty folder still appears.
		folders, err := m.Folders(ctx)
		if err != nil {
			t.Fatalf("Folders: %v", err)
		}
		if len(folders) != 3 {
			t.Fatalf("got %d folders, want 3", len(folders))
		}
		// Sorted by name: Documents, Photos, Photos.
		if folders[0].Name != "Documents" {
			t.Errorf("first folder = %q, want %q (sorted by name)", folders[0].Name, "Documents")
		}
		for _, f := range folders {
			if f.Count != 0 {
				t.Errorf("folder %q count = %d, want 0", f.Name, f.Count)
			}
		}

		// File something and re-read the counts.
		md, err := m.Upload(ctx, "in-folder.png", testImage(t, 10, 10, "png"), 0, &photos.ID)
		if err != nil {
			t.Fatalf("Upload(into folder): %v", err)
		}
		folders, err = m.Folders(ctx)
		if err != nil {
			t.Fatalf("Folders: %v", err)
		}
		for _, f := range folders {
			want := 0
			if f.ID == photos.ID {
				want = 1
			}
			if f.Count != want {
				t.Errorf("folder %q (id %d) count = %d, want %d", f.Name, f.ID, f.Count, want)
			}
		}
		_ = docs

		// Moving between folders and back to unfiled.
		if err := m.Move(ctx, md.ID, nil); err != nil {
			t.Fatalf("Move(unfiled): %v", err)
		}
		got, err := m.GetByID(ctx, md.ID, "en")
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.FolderID != nil {
			t.Errorf("folder_id = %v, want nil after moving to unfiled", got.FolderID)
		}
		if err := m.Move(ctx, md.ID, &photos.ID); err != nil {
			t.Fatalf("Move(into folder): %v", err)
		}

		// A folder with files in it is refused, and nothing about it moves.
		// Deleting one used to unfile its contents, scattering in a click
		// the files an editor had just gathered.
		if err := m.DeleteFolder(ctx, photos.ID); !errors.Is(err, ErrFolderNotEmpty) {
			t.Errorf("DeleteFolder(non-empty) = %v, want ErrFolderNotEmpty", err)
		}
		got, err = m.GetByID(ctx, md.ID, "en")
		if err != nil {
			t.Fatalf("GetByID after refused DeleteFolder: %v", err)
		}
		if got.FolderID == nil || *got.FolderID != photos.ID {
			t.Errorf("folder_id = %v, want the file still filed in %d", got.FolderID, photos.ID)
		}
		folders, err = m.Folders(ctx)
		if err != nil {
			t.Fatalf("Folders after refused DeleteFolder: %v", err)
		}
		found := false
		for _, f := range folders {
			if f.ID == photos.ID {
				found = true
			}
		}
		if !found {
			t.Error("the folder was deleted even though it still held a file")
		}

		// Emptied, the same folder goes.
		if err := m.Move(ctx, md.ID, nil); err != nil {
			t.Fatalf("Move(unfiled): %v", err)
		}
		if err := m.DeleteFolder(ctx, photos.ID); err != nil {
			t.Fatalf("DeleteFolder(empty): %v", err)
		}
		folders, err = m.Folders(ctx)
		if err != nil {
			t.Fatalf("Folders after DeleteFolder: %v", err)
		}
		for _, f := range folders {
			if f.ID == photos.ID {
				t.Error("the emptied folder is still there after being deleted")
			}
		}

		// Deleting it again is not an error: two tabs can both submit.
		if err := m.DeleteFolder(ctx, photos.ID); err != nil {
			t.Errorf("DeleteFolder(already gone) = %v, want nil", err)
		}
	})
}

func TestMediaFolderNameCaseSensitivity(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		if _, err := m.CreateFolder(ctx, "Photos", KindImage); err != nil {
			t.Fatalf("CreateFolder: %v", err)
		}
		// Folder names are free text typed by an editor, so unlike slugs and
		// emails they are not normalized before they reach the unique index.
		// Postgres compares them case-sensitively; MySQL's default collation
		// does not, and the schema pins a binary collation on this column so
		// both engines agree that "photos" is a different folder.
		if _, err := m.CreateFolder(ctx, "photos", KindImage); err != nil {
			t.Errorf("CreateFolder(differing only in case) = %v, want it accepted on every engine", err)
		}
	})
}

func TestMediaListByFolder(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		folder, err := m.CreateFolder(ctx, "Album", KindImage)
		if err != nil {
			t.Fatalf("CreateFolder: %v", err)
		}
		filed, err := m.Upload(ctx, "filed.png", testImage(t, 10, 10, "png"), 0, &folder.ID)
		if err != nil {
			t.Fatalf("Upload(filed): %v", err)
		}
		unfiled, err := m.Upload(ctx, "unfiled.png", testImage(t, 10, 10, "png"), 0, nil)
		if err != nil {
			t.Fatalf("Upload(unfiled): %v", err)
		}

		inFolder, err := m.All(ctx, "en", ListOptions{FolderID: &folder.ID})
		if err != nil {
			t.Fatalf("All(folder): %v", err)
		}
		if len(inFolder) != 1 || inFolder[0].ID != filed.ID {
			t.Errorf("All(folder) returned %d items, want just the filed one", len(inFolder))
		}

		none, err := m.All(ctx, "en", ListOptions{Unfiled: true})
		if err != nil {
			t.Fatalf("All(unfiled): %v", err)
		}
		if len(none) != 1 || none[0].ID != unfiled.ID {
			t.Errorf("All(unfiled) returned %d items, want just the unfiled one", len(none))
		}

		// FolderID takes precedence over Unfiled, as documented.
		both, err := m.All(ctx, "en", ListOptions{FolderID: &folder.ID, Unfiled: true})
		if err != nil {
			t.Fatalf("All(folder+unfiled): %v", err)
		}
		if len(both) != 1 || both[0].ID != filed.ID {
			t.Errorf("All(folder+unfiled) returned %d items, want the folder filter to win", len(both))
		}
	})
}

func TestMediaUploadedByReferencesUser(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		// A real user, since uploaded_by is a foreign key into cms_users.
		// Seeded through the auth store rather than raw SQL, so this test
		// stays dialect-agnostic.
		uploader := &auth.User{Email: "uploader@example.com", Name: "Uploader",
			PasswordHash: "x", Role: auth.RoleEditor, Active: true}
		userID, err := auth.NewStore(db).Insert(ctx, uploader)
		if err != nil {
			t.Fatalf("seeding user: %v", err)
		}

		md, err := m.Upload(ctx, "mine.png", testImage(t, 10, 10, "png"), userID, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		got, err := m.GetByID(ctx, md.ID, "en")
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.UploadedBy == nil || *got.UploadedBy != userID {
			t.Errorf("uploaded_by = %v, want %d", got.UploadedBy, userID)
		}
	})
}
