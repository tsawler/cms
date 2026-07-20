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
	if len(p.Variants) != 2 {
		t.Fatalf("got %d variants, want 2", len(p.Variants))
	}

	web, err := decodeVariant(p.Variants[0].Data)
	if err != nil {
		t.Fatalf("decoding web variant: %v", err)
	}
	if web.Bounds().Dx() != webMaxWidth {
		t.Errorf("web width = %d, want %d", web.Bounds().Dx(), webMaxWidth)
	}

	thumb, err := decodeVariant(p.Variants[1].Data)
	if err != nil {
		t.Fatalf("decoding thumb variant: %v", err)
	}
	if thumb.Bounds().Dx() > thumbSize || thumb.Bounds().Dy() > thumbSize {
		t.Errorf("thumb %v exceeds %dpx bound", thumb.Bounds(), thumbSize)
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
