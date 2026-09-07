package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// fill paints a whole image one colour.
func fill(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestAverageBrightness(t *testing.T) {
	cases := []struct {
		name string
		img  image.Image
		dark bool
	}{
		{"black", fill(32, 32, color.RGBA{0, 0, 0, 255}), true},
		{"white", fill(32, 32, color.RGBA{255, 255, 255, 255}), false},
		// Night sky and a sunlit field: the two the banner text has to
		// read against.
		{"navy", fill(32, 32, color.RGBA{16, 24, 48, 255}), true},
		{"wheat", fill(32, 32, color.RGBA{222, 202, 135, 255}), false},
		// Pure blue is dark to the eye despite a full channel; pure green
		// is not. A plain channel average would get both wrong.
		{"blue", fill(32, 32, color.RGBA{0, 0, 255, 255}), true},
		{"green", fill(32, 32, color.RGBA{0, 255, 0, 255}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := averageBrightness(c.img) < darkThreshold
			if got != c.dark {
				t.Errorf("dark = %v, want %v (brightness %.1f)", got, c.dark, averageBrightness(c.img))
			}
		})
	}
}

// A fully transparent image shows whatever is behind it, so counting its
// pixels as black would call every transparent PNG dark.
func TestAverageBrightnessSkipsTransparentPixels(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	if got := averageBrightness(img); got != 255 {
		t.Errorf("fully transparent brightness = %.1f, want 255", got)
	}

	// Half transparent, half white: only the opaque half counts, so the
	// result is white rather than an average pulled down by the void.
	half := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := 16; x < 32; x++ {
			half.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	if got := averageBrightness(half); got < 254 {
		t.Errorf("half-transparent white brightness = %.1f, want ~255", got)
	}
}

// Alpha is premultiplied in RGBA(): a half-transparent white must not read
// as grey once it is undone.
func TestAverageBrightnessUndoesPremultipliedAlpha(t *testing.T) {
	img := fill(32, 32, color.RGBA{128, 128, 128, 128}) // premultiplied white
	if got := averageBrightness(img); got < 254 {
		t.Errorf("translucent white brightness = %.1f, want ~255", got)
	}
}

// storeThumb puts an encoded PNG where IsDark looks for it: the thumb
// rendition of md's object set, under the manager's key root.
func storeThumb(t *testing.T, m *Manager, s *memStore, md *Media, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding the thumbnail: %v", err)
	}
	key := m.abs(md.StoreKey + "/thumb" + md.VariantExt)
	if err := s.Put(context.Background(), key, "image/png", &buf); err != nil {
		t.Fatalf("storing the thumbnail: %v", err)
	}
}

// IsDark is what decides whether banner text over a picture is set light
// or dark, so it has to reach the right object and it has to fail softly:
// every error path returns "not dark", which leaves the text in the site's
// own colour rather than making it invisible.
func TestIsDark(t *testing.T) {
	store := newMemStore()
	m := NewManager(nil, store, slog.New(slog.NewTextHandler(io.Discard, nil)))

	dark := &Media{Kind: KindImage, StoreKey: "abc/night", VariantExt: ".png"}
	light := &Media{Kind: KindImage, StoreKey: "def/noon", VariantExt: ".png"}
	storeThumb(t, m, store, dark, fill(16, 16, color.RGBA{16, 24, 48, 255}))
	storeThumb(t, m, store, light, fill(16, 16, color.RGBA{240, 240, 235, 255}))

	got, err := m.IsDark(context.Background(), dark)
	if err != nil {
		t.Fatalf("IsDark(night): %v", err)
	}
	if !got {
		t.Error("IsDark(night) = false, want true")
	}

	got, err = m.IsDark(context.Background(), light)
	if err != nil {
		t.Fatalf("IsDark(noon): %v", err)
	}
	if got {
		t.Error("IsDark(noon) = true, want false")
	}
}

// Anything without a raster rendition to measure is ErrNoRaster rather
// than a guess: a vector has no pixels, a video without a poster has no
// variants, and a nil record has nothing at all.
func TestIsDarkWithoutARaster(t *testing.T) {
	m := NewManager(nil, newMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	cases := map[string]*Media{
		"nil record":      nil,
		"vector":          {Kind: KindImage, StoreKey: "abc/logo", VariantExt: ""},
		"file":            {Kind: KindFile, StoreKey: "abc/terms", VariantExt: ".png"},
		"video no poster": {Kind: KindVideo, StoreKey: "abc/clip", VariantExt: ""},
	}
	for name, md := range cases {
		t.Run(name, func(t *testing.T) {
			dark, err := m.IsDark(context.Background(), md)
			if !errors.Is(err, ErrNoRaster) {
				t.Errorf("err = %v, want ErrNoRaster", err)
			}
			if dark {
				t.Error("dark = true, want false alongside an error")
			}
		})
	}
}

// A record whose thumbnail is missing from the bucket — an interrupted
// upload, an object deleted underneath us — surfaces the store's error
// rather than reporting a dark image.
func TestIsDarkMissingObject(t *testing.T) {
	m := NewManager(nil, newMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	md := &Media{Kind: KindImage, StoreKey: "abc/gone", VariantExt: ".png"}

	dark, err := m.IsDark(context.Background(), md)
	if !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("err = %v, want ErrObjectNotFound", err)
	}
	if dark {
		t.Error("dark = true, want false alongside an error")
	}
}

// Bytes that are not an image at all — a truncated write, or a rendition
// that was never finished — must come back as a decode error, not a
// panic and not a verdict.
func TestIsDarkUndecodableObject(t *testing.T) {
	store := newMemStore()
	m := NewManager(nil, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	md := &Media{Kind: KindImage, StoreKey: "abc/broken", VariantExt: ".png"}
	if err := store.Put(context.Background(), m.abs(md.StoreKey+"/thumb.png"),
		"image/png", strings.NewReader("not an image")); err != nil {
		t.Fatalf("storing the thumbnail: %v", err)
	}

	if dark, err := m.IsDark(context.Background(), md); err == nil || dark {
		t.Errorf("IsDark(undecodable) = %v, %v; want false and an error", dark, err)
	}
}
