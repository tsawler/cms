package media

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func testImage(t *testing.T, width, height int, encodeAs string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x += 10 {
		for y := range height {
			img.Set(x, y, color.RGBA{R: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	var err error
	switch encodeAs {
	case "png":
		err = png.Encode(&buf, img)
	default:
		err = jpeg.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatalf("encoding test image: %v", err)
	}
	return buf.Bytes()
}

func TestProcessJPEGProducesVariants(t *testing.T) {
	data := testImage(t, 2000, 1000, "jpeg")
	p, err := process(data, "image/jpeg", DefaultWebPQuality)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if p.Width != 2000 || p.Height != 1000 {
		t.Errorf("dims = %dx%d, want 2000x1000", p.Width, p.Height)
	}
	if p.Ext != ".jpg" {
		t.Errorf("ext = %q, want .jpg", p.Ext)
	}
	if len(p.Variants) != len(imageVariants) {
		t.Fatalf("got %d variants, want one per rung (%d)", len(p.Variants), len(imageVariants))
	}

	// Every rung is decodable WebP at exactly the size variantSize
	// promised — the numbers ImageFor reports as an <img>'s width and
	// height, so they had better be the ones actually encoded.
	byName := map[string]variant{}
	for _, v := range p.Variants {
		img, err := decodeVariant(v.Data)
		if err != nil {
			t.Fatalf("decoding %s variant: %v", v.Name, err)
		}
		if img.Bounds().Dx() != v.Width || img.Bounds().Dy() != v.Height {
			t.Errorf("%s variant encoded %dx%d, recorded %dx%d",
				v.Name, img.Bounds().Dx(), img.Bounds().Dy(), v.Width, v.Height)
		}
		byName[v.Name] = v
	}

	if got := byName["web"].Width; got != webMaxWidth {
		t.Errorf("web width = %d, want %d", got, webMaxWidth)
	}
	if got := byName["card"].Width; got != cardMaxWidth {
		t.Errorf("card width = %d, want %d", got, cardMaxWidth)
	}
	if thumb := byName["thumb"]; thumb.Width > thumbSize || thumb.Height > thumbSize {
		t.Errorf("thumb %dx%d exceeds %dpx bound", thumb.Width, thumb.Height, thumbSize)
	}
	// The ladder is only worth having if the rungs really differ in size.
	if byName["card"].Width >= byName["web"].Width {
		t.Errorf("card (%d bytes) is not smaller than web (%d bytes)",
			len(byName["card"].Data), len(byName["web"].Data))
	}
}

func TestVariantSize(t *testing.T) {
	web := variantSpec{Name: "web", MaxWidth: 1600}
	card := variantSpec{Name: "card", MaxWidth: 800}
	thumb := variantSpec{Name: "thumb", Box: 320}

	tests := []struct {
		name       string
		spec       variantSpec
		srcW, srcH int
		wantW      int
		wantH      int
	}{
		{"landscape down to card", card, 2000, 1000, 800, 400},
		{"landscape down to web", web, 2000, 1000, 1600, 800},
		{"landscape fits the thumb box on width", thumb, 2000, 1000, 320, 160},
		{"portrait fits the thumb box on height", thumb, 1000, 2000, 160, 320},
		{"already smaller is never upscaled", web, 640, 480, 640, 480},
		{"exactly at the bound is left alone", card, 800, 600, 800, 600},
		{"unknown source size stays unknown", card, 0, 0, 0, 0},
		{"a sliver never rounds away to zero", card, 4000, 3, 800, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h := variantSize(tt.srcW, tt.srcH, tt.spec)
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("variantSize(%d, %d, %s) = %dx%d, want %dx%d",
					tt.srcW, tt.srcH, tt.spec.Name, w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

// A video's poster has no stored original to rebuild from, so it gets only
// the renditions a player and the library actually use.
func TestProcessPosterSkipsTheCardRendition(t *testing.T) {
	p, err := processPoster(testImage(t, 1920, 1080, "jpeg"), "image/jpeg", DefaultWebPQuality)
	if err != nil {
		t.Fatalf("processPoster: %v", err)
	}
	if len(p.Variants) != 2 {
		t.Fatalf("got %d variants, want web and thumb", len(p.Variants))
	}
	for _, v := range p.Variants {
		if v.Name == "card" {
			t.Error("a poster produced a card rendition")
		}
	}
}

func TestProcessSmallImageNotUpscaled(t *testing.T) {
	data := testImage(t, 800, 600, "png")
	p, err := process(data, "image/png", DefaultWebPQuality)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	web, err := decodeVariant(p.Variants[0].Data)
	if err != nil {
		t.Fatalf("decoding web variant: %v", err)
	}
	if web.Bounds().Dx() != 800 {
		t.Errorf("web width = %d, want original 800 (no upscaling)", web.Bounds().Dx())
	}
	if p.Variants[0].Ext != ".webp" {
		t.Errorf("png variant ext = %q, want .webp", p.Variants[0].Ext)
	}
}

func TestProcessRejectsNonImages(t *testing.T) {
	if _, err := process([]byte("just some text, definitely not an image"), "text/plain", DefaultWebPQuality); err == nil {
		t.Fatal("expected error for non-image upload")
	}
	// Right mime, corrupt body.
	if _, err := process([]byte("\xff\xd8\xffgarbage"), "image/jpeg", DefaultWebPQuality); err == nil {
		t.Fatal("expected error for corrupt image data")
	}
}

func decodeVariant(data []byte) (image.Image, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err == nil && format != "webp" {
		return nil, errors.New("variant is " + format + ", want webp")
	}
	return img, err
}

func TestProcessQualityAffectsSize(t *testing.T) {
	data := testImage(t, 1200, 900, "jpeg")
	low, err := process(data, "image/jpeg", 0.1)
	if err != nil {
		t.Fatalf("process q=0.1: %v", err)
	}
	high, err := process(data, "image/jpeg", 0.9)
	if err != nil {
		t.Fatalf("process q=0.9: %v", err)
	}
	if len(low.Variants[0].Data) >= len(high.Variants[0].Data) {
		t.Errorf("web variant at q=0.1 (%d bytes) not smaller than q=0.9 (%d bytes)",
			len(low.Variants[0].Data), len(high.Variants[0].Data))
	}
}
