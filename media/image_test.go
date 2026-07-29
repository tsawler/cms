package media

import (
	"strings"
	"testing"
)

// newURLManager returns a Manager with no database, for the URL-building
// tests: ImageFor reads only the object store and the key root.
func newURLManager() *Manager {
	return NewManager(nil, newMemStore(), newTestLogger())
}

func TestImageForBuildsASrcsetAcrossTheLadder(t *testing.T) {
	m := newURLManager()
	md := &Media{
		Kind: KindImage, StoreKey: "abc123", Ext: ".jpg", VariantExt: ".webp",
		Width: 2400, Height: 1200, Alt: "A wide photo",
	}

	img := m.ImageFor(md, "card")
	if img == nil {
		t.Fatal("ImageFor returned nil for an image")
	}
	// The default src is the rendition asked for, at the size that
	// rendition really is — not the original's 2400x1200.
	if want := "/media/abc123/card.webp"; img.URL != want {
		t.Errorf("URL = %q, want %q", img.URL, want)
	}
	if img.Width != 800 || img.Height != 400 {
		t.Errorf("dimensions = %dx%d, want 800x400", img.Width, img.Height)
	}
	if img.Alt != "A wide photo" {
		t.Errorf("alt = %q, want the record's", img.Alt)
	}

	want := "/media/abc123/web.webp 1600w, /media/abc123/card.webp 800w, /media/abc123/thumb.webp 320w"
	if img.Srcset != want {
		t.Errorf("srcset =\n  %q\nwant\n  %q", img.Srcset, want)
	}

	// Asking for the header slot moves the default src up a rung without
	// changing the candidates.
	header := m.ImageFor(md, "web")
	if want := "/media/abc123/web.webp"; header.URL != want {
		t.Errorf("web URL = %q, want %q", header.URL, want)
	}
	if header.Width != 1600 || header.Height != 800 {
		t.Errorf("web dimensions = %dx%d, want 1600x800", header.Width, header.Height)
	}
	if header.Srcset != img.Srcset {
		t.Error("the srcset changed with the preferred rendition")
	}
}

// An image smaller than the ladder's bounds is not upscaled, so its larger
// rungs are all the same picture and belong in the srcset once.
func TestImageForDedupesRungsOfTheSameWidth(t *testing.T) {
	m := newURLManager()
	md := &Media{
		Kind: KindImage, StoreKey: "small1", Ext: ".png", VariantExt: ".webp",
		Width: 600, Height: 400,
	}

	img := m.ImageFor(md, "card")
	if n := len(img.Renditions); n != 2 {
		t.Fatalf("got %d renditions, want 2 (600 and 320 wide): %v", n, img.Renditions)
	}
	if strings.Count(img.Srcset, "600w") != 1 {
		t.Errorf("srcset %q lists the 600w candidate more than once", img.Srcset)
	}
	// The default src still honours the request: card and web are the same
	// size here, but card is the object the caller asked for.
	if want := "/media/small1/card.webp"; img.URL != want {
		t.Errorf("URL = %q, want %q", img.URL, want)
	}
	if img.Width != 600 || img.Height != 400 {
		t.Errorf("dimensions = %dx%d, want the original 600x400", img.Width, img.Height)
	}
}

// A single candidate tells the browser nothing src has not already, so no
// srcset is written at all.
func TestImageForOmitsAPointlessSrcset(t *testing.T) {
	m := newURLManager()
	md := &Media{
		Kind: KindImage, StoreKey: "tiny01", Ext: ".png", VariantExt: ".webp",
		Width: 100, Height: 80,
	}
	if img := m.ImageFor(md, "card"); img.Srcset != "" {
		t.Errorf("srcset = %q, want none for a one-rendition image", img.Srcset)
	}
}

func TestImageForVectorsAndNonImages(t *testing.T) {
	m := newURLManager()

	svg := m.ImageFor(&Media{
		Kind: KindImage, StoreKey: "vec001", Ext: ".svg", VariantExt: ".svg",
		Width: 48, Height: 48,
	}, "card")
	// Alternate widths of a vector would be the same bytes under different
	// names, so it gets one URL and its own intrinsic size.
	if svg.Srcset != "" {
		t.Errorf("svg srcset = %q, want none", svg.Srcset)
	}
	if want := "/media/vec001/web.svg"; svg.URL != want {
		t.Errorf("svg URL = %q, want %q", svg.URL, want)
	}
	if svg.Width != 48 || svg.Height != 48 {
		t.Errorf("svg dimensions = %dx%d, want 48x48", svg.Width, svg.Height)
	}

	if got := m.ImageFor(nil, "card"); got != nil {
		t.Error("ImageFor(nil) should be nil, so templates render nothing")
	}
	if got := m.ImageFor(&Media{Kind: KindVideo, StoreKey: "vid001"}, "card"); got != nil {
		t.Error("ImageFor should be nil for a video")
	}
	if got := m.ImageFor(&Media{Kind: KindFile, StoreKey: "doc001/report.pdf"}, "card"); got != nil {
		t.Error("ImageFor should be nil for a document")
	}
}

// Dimensions are unknown on records written before they were recorded.
// The image still has to render; it just cannot promise a size.
func TestImageForWithoutDimensions(t *testing.T) {
	m := newURLManager()
	img := m.ImageFor(&Media{
		Kind: KindImage, StoreKey: "old001", Ext: ".jpg", VariantExt: ".webp",
	}, "card")
	if want := "/media/old001/card.webp"; img.URL != want {
		t.Errorf("URL = %q, want %q", img.URL, want)
	}
	if img.Width != 0 || img.Height != 0 || img.Srcset != "" {
		t.Errorf("got %dx%d srcset %q, want no claims about size",
			img.Width, img.Height, img.Srcset)
	}
}

// An unknown rendition name falls back to the full-width one rather than
// building a URL for an object that cannot exist.
func TestImageForUnknownRendition(t *testing.T) {
	m := newURLManager()
	img := m.ImageFor(&Media{
		Kind: KindImage, StoreKey: "abc123", Ext: ".jpg", VariantExt: ".webp",
		Width: 2400, Height: 1200,
	}, "gigantic")
	if want := "/media/abc123/web.webp"; img.URL != want {
		t.Errorf("URL = %q, want %q", img.URL, want)
	}
}
