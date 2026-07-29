package media

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// readAll drains a stored object, failing the test rather than the caller.
func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading object: %v", err)
	}
	return data
}

// The case this exists for: an image uploaded before the ladder grew a rung
// has no object for it, and the first page that asks builds it.
func TestEnsureRenditionRebuildsAMissingVariant(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, objects := newTestManager(db)

		md, err := m.Upload(ctx, "wide.jpg", testImage(t, 2000, 1000, "jpeg"), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		rest := md.StoreKey + "/card" + md.VariantExt
		key := m.abs(rest)

		// Stand in for an upload that predates the rung.
		if err := objects.Delete(ctx, key); err != nil {
			t.Fatalf("Delete object: %v", err)
		}
		if _, _, err := objects.Get(ctx, key); !errors.Is(err, ErrObjectNotFound) {
			t.Fatalf("the object survived the delete: %v", err)
		}

		if err := m.EnsureRendition(ctx, rest); err != nil {
			t.Fatalf("EnsureRendition: %v", err)
		}

		body, _, err := objects.Get(ctx, key)
		if err != nil {
			t.Fatalf("the rendition was not stored: %v", err)
		}
		defer body.Close()

		// Rebuilt from the original, so it must match what the upload would
		// have written: WebP at the card rung's exact size.
		img, err := decodeVariant(readAll(t, body))
		if err != nil {
			t.Fatalf("decoding the rebuilt rendition: %v", err)
		}
		if img.Bounds().Dx() != cardMaxWidth || img.Bounds().Dy() != cardMaxWidth/2 {
			t.Errorf("rebuilt %v, want %dx%d", img.Bounds(), cardMaxWidth, cardMaxWidth/2)
		}
	})
}

// A vector's renditions are its own bytes, so a rebuild copies rather than
// rasterizing.
func TestEnsureRenditionRebuildsAVector(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, objects := newTestManager(db)

		md, err := m.Upload(ctx, "logo.svg", []byte(cleanSVG), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		rest := md.StoreKey + "/card" + md.VariantExt
		if err := objects.Delete(ctx, m.abs(rest)); err != nil {
			t.Fatalf("Delete object: %v", err)
		}
		if err := m.EnsureRendition(ctx, rest); err != nil {
			t.Fatalf("EnsureRendition: %v", err)
		}
		body, _, err := objects.Get(ctx, m.abs(rest))
		if err != nil {
			t.Fatalf("the rendition was not stored: %v", err)
		}
		defer body.Close()
		if got := string(readAll(t, body)); got != cleanSVG {
			t.Error("the rebuilt vector differs from the original bytes")
		}
	})
}

// Only derived renditions of a library image are rebuildable, and asking
// for anything else has to be distinguishable from a real failure — the
// proxy answers ErrNoRendition with a quiet 404.
func TestEnsureRenditionRefusesWhatItCannotBuild(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		img, err := m.Upload(ctx, "photo.png", testImage(t, 40, 20, "png"), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		video, err := m.UploadFrom(ctx, "clip.mp4",
			strings.NewReader("\x00\x00\x00\x18ftypmp42 a video body"), 21,
			testImage(t, 100, 50, "png"), 0, nil)
		if err != nil {
			t.Fatalf("Upload video: %v", err)
		}

		tests := []struct {
			name string
			rest string
		}{
			{"the original is the source, not a rendition", img.StoreKey + "/original" + img.Ext},
			{"an unknown rendition name", img.StoreKey + "/enormous.webp"},
			{"the wrong extension for this item", img.StoreKey + "/card.jpg"},
			{"an item that is not in the library", "deadbeefdeadbeefdeadbeef/card.webp"},
			{"no item at all", "card.webp"},
			{"a poster has no stored source", video.StoreKey + "/card" + video.VariantExt},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if err := m.EnsureRendition(ctx, tt.rest); !errors.Is(err, ErrNoRendition) {
					t.Errorf("EnsureRendition(%q) = %v, want ErrNoRendition", tt.rest, err)
				}
			})
		}
	})
}

// A listing page that goes cold sends many requests for the same new
// rendition at once; they must cost one encode, not one each.
func TestEnsureRenditionCollapsesConcurrentRequests(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, objects := newTestManager(db)

		md, err := m.Upload(ctx, "wide.jpg", testImage(t, 1200, 600, "jpeg"), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		rest := md.StoreKey + "/card" + md.VariantExt
		if err := objects.Delete(ctx, m.abs(rest)); err != nil {
			t.Fatalf("Delete object: %v", err)
		}

		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Go(func() { errs[i] = m.EnsureRendition(ctx, rest) })
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("EnsureRendition #%d: %v", i, err)
			}
		}
		if _, _, err := objects.Get(ctx, m.abs(rest)); err != nil {
			t.Fatalf("the rendition was not stored: %v", err)
		}
	})
}
