package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"strings"
	"testing"
	"time"
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

// The two ways a file can fail to be read as its own type are told apart
// from "we don't take those", and both are findable with errors.Is. The
// admin used to pick the message by searching the error text for
// "decoding image" and "parsing svg", which is a coupling nothing
// enforces: rewording either message would have turned a bad upload into
// a 500.
func TestUndecodableFilesAreDistinguishable(t *testing.T) {
	// A PNG header with nothing behind it: the type is one we handle,
	// the bytes are not that type.
	truncated := append([]byte("\x89PNG\r\n\x1a\n"), 0, 1, 2, 3)
	if _, err := process(truncated, "image/png", DefaultWebPQuality); !errors.Is(err, ErrUndecodable) {
		t.Errorf("truncated png: err = %v, want ErrUndecodable", err)
	} else if errors.Is(err, ErrUnsupportedType) {
		t.Error("a damaged file of a supported type reads as an unsupported one")
	}

	if _, err := processSVG([]byte(`<svg><unclosed>`)); !errors.Is(err, ErrUndecodable) {
		t.Errorf("malformed svg: err = %v, want ErrUndecodable", err)
	}

	// And a type we genuinely do not handle stays its own answer.
	if _, err := process([]byte("whatever"), "application/x-tar", DefaultWebPQuality); !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("unsupported mime: err = %v, want ErrUnsupportedType", err)
	} else if errors.Is(err, ErrUndecodable) {
		t.Error("an unsupported type reads as a damaged one")
	}

	// The detail survives, so a log line still says which file and why.
	_, err := process(truncated, "image/png", DefaultWebPQuality)
	if !strings.Contains(err.Error(), "decoding image") {
		t.Errorf("the wrapped detail was lost: %v", err)
	}
}

// pngDeclaring returns a PNG that is nothing but a header declaring
// width×height RGBA pixels — a few dozen bytes that a decoder trusting
// the header would allocate width×height×4 bytes for.
func pngDeclaring(width, height uint32) []byte {
	ihdr := binary.BigEndian.AppendUint32(nil, width)
	ihdr = binary.BigEndian.AppendUint32(ihdr, height)
	ihdr = append(ihdr, 8, 6, 0, 0, 0) // 8-bit RGBA, no interlace
	chunk := func(kind string, data []byte) []byte {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
		out = append(out, kind...)
		out = append(out, data...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	}
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, chunk("IHDR", ihdr)...)
	return append(out, chunk("IEND", nil)...)
}

// An image is refused on the pixels its header declares, before anything
// is allocated for them: the byte cap alone let a tiny PNG declaring
// 60000×60000 pixels ask the decoder for some 14 GB.
func TestProcessRejectsImagesOverThePixelCap(t *testing.T) {
	bomb := pngDeclaring(60000, 60000)
	for name, run := range map[string]func([]byte) error{
		"process": func(b []byte) error { _, err := process(b, "image/png", DefaultWebPQuality); return err },
		"poster":  func(b []byte) error { _, err := processPoster(b, "image/png", DefaultWebPQuality); return err },
	} {
		err := run(bomb)
		if !errors.Is(err, ErrTooManyPixels) {
			t.Errorf("%s: err = %v, want ErrTooManyPixels", name, err)
		}
		// Callers that only know the byte caps still see "too large".
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: err = %v, want it to match ErrTooLarge too", name, err)
		}
		if errors.Is(err, ErrUndecodable) {
			t.Errorf("%s: an oversized image reads as a damaged one", name)
		}
	}

	// The cap itself is allowed: a header declaring exactly MaxImagePixels
	// gets past the check, and fails only because no pixel data follows.
	side := uint32(math.Sqrt(MaxImagePixels))
	if side*side != MaxImagePixels {
		t.Fatalf("MaxImagePixels %d is not a square; pick another boundary", MaxImagePixels)
	}
	if _, err := process(pngDeclaring(side, side), "image/png", DefaultWebPQuality); errors.Is(err, ErrTooManyPixels) {
		t.Errorf("an image of exactly MaxImagePixels was refused: %v", err)
	}
}

// Every decode of an upload waits for one of the image slots, so a burst
// of large uploads can't decode more images at once than the slots allow.
// With every slot held, an upload gives up when its context does rather
// than decoding anyway.
func TestImageDecodesWaitForASlot(t *testing.T) {
	store := newMemStore()
	m := NewManager(nil, store, newTestLogger())
	for range cap(m.imageSlots) {
		m.imageSlots <- struct{}{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := m.Upload(ctx, "photo.png", testImage(t, 40, 20, "png"), 0, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("upload with every slot held: err = %v, want context.DeadlineExceeded", err)
	}
	if _, err := m.slottedPoster(ctx, testImage(t, 40, 20, "png")); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("poster with every slot held: err = %v, want context.DeadlineExceeded", err)
	}

	// Freeing a slot lets the next decode through.
	<-m.imageSlots
	p, err := m.slottedPoster(context.Background(), testImage(t, 40, 20, "png"))
	if err != nil || p.Width != 40 {
		t.Fatalf("poster with a slot free: %+v, %v", p, err)
	}
	if len(m.imageSlots) != cap(m.imageSlots)-1 {
		t.Errorf("a finished decode kept its slot: %d of %d held", len(m.imageSlots), cap(m.imageSlots))
	}
}
