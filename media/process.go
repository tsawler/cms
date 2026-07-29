package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"

	"github.com/disintegration/imaging"
	"github.com/gen2brain/webp"
	_ "golang.org/x/image/webp" // decode-only
)

// ErrUnsupportedType is returned for uploads that are neither a supported
// image (JPEG, PNG, GIF, WebP) nor a whitelisted document type (PDF,
// office formats, text/CSV, ZIP).
var ErrUnsupportedType = errors.New("media: unsupported file type")

const (
	// webMaxWidth is the largest width served to pages: the rendition a
	// header image or a full-width body image uses.
	webMaxWidth = 1600
	// cardMaxWidth bounds the listing-card rendition — a blog or news
	// card is a few hundred CSS pixels wide, so this covers it at 2×
	// without shipping the full-width image to fill a thumbnail slot.
	cardMaxWidth = 800
	// thumbSize bounds the admin media-library thumbnail.
	thumbSize = 320

	// DefaultWebPQuality is the lossy WebP quality, on a 0–1 scale, used
	// for the derived variants unless the host configures another
	// (cms.Config.MediaWebPQuality). Deliberately low: variants exist for
	// fast page loads, and the untouched original is always kept.
	DefaultWebPQuality = 0.3
)

var allowedMimes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// variantSpec is one rung of the rendition ladder: a name, which becomes
// the object's filename, and the bound it is resized to. MaxWidth caps the
// width and lets the height follow the aspect ratio; Box fits the whole
// image inside a square, which is what a uniform thumbnail grid wants.
// Exactly one of the two is set.
type variantSpec struct {
	Name     string
	MaxWidth int
	Box      int
}

// imageVariants is the ladder every raster image upload produces, largest
// first — the order srcsets are written in. Adding a rung here is enough to
// make new uploads produce it; existing uploads grow it on first request,
// through Manager.EnsureRendition.
var imageVariants = []variantSpec{
	{Name: "web", MaxWidth: webMaxWidth},
	{Name: "card", MaxWidth: cardMaxWidth},
	{Name: "thumb", Box: thumbSize},
}

// posterVariants is the shorter ladder a video's poster frame gets. A
// player needs one full-size still and the library needs a thumbnail;
// nothing lays a video out with a srcset, and a poster has no stored
// original to rebuild further renditions from.
var posterVariants = []variantSpec{
	{Name: "web", MaxWidth: webMaxWidth},
	{Name: "thumb", Box: thumbSize},
}

// variantSpecFor returns the image ladder's rung of that name.
func variantSpecFor(name string) (variantSpec, bool) {
	for _, spec := range imageVariants {
		if spec.Name == name {
			return spec, true
		}
	}
	return variantSpec{}, false
}

// variantSize returns the exact pixel size one rendition of a srcW×srcH
// image will have. Nothing is ever upscaled, so an image already smaller
// than the rung's bound keeps its own size.
//
// It is the single source of truth for those numbers: process resizes to
// what it returns, and ImageFor reports the same figures as an <img>'s
// width and height, so the browser reserves exactly the space the bytes
// will occupy. A zero source dimension (an SVG with no size, a posterless
// video) yields zeroes, meaning "unknown".
func variantSize(srcW, srcH int, spec variantSpec) (int, int) {
	if srcW <= 0 || srcH <= 0 {
		return 0, 0
	}
	scale := float64(spec.MaxWidth) / float64(srcW)
	if spec.Box > 0 {
		scale = math.Min(
			float64(spec.Box)/float64(srcW),
			float64(spec.Box)/float64(srcH))
	}
	if scale >= 1 {
		return srcW, srcH
	}
	return max(int(math.Round(float64(srcW)*scale)), 1),
		max(int(math.Round(float64(srcH)*scale)), 1)
}

// variant is one derived rendition of an upload.
type variant struct {
	Name   string // "web", "card", or "thumb"
	Ext    string // with dot
	Mime   string
	Width  int
	Height int
	Data   []byte
}

// processed is the result of decoding and resizing an upload. The original
// bytes are stored untouched (preserving GIF animation, EXIF, etc.); only
// the variants are re-encoded.
type processed struct {
	Width      int
	Height     int
	Ext        string // original's extension, with dot
	VariantExt string // extension shared by all variants, with dot
	Variants   []variant
}

// process validates and decodes an uploaded image and produces its resized
// variants, encoded as lossy WebP at the given 0–1 quality (alpha
// survives, so PNG sources keep their transparency).
func process(data []byte, mime string, quality float64) (*processed, error) {
	return processVariants(data, mime, quality, imageVariants)
}

// processPoster is process for a video's client-captured poster frame,
// which needs only the renditions a player and the library use.
func processPoster(data []byte, mime string, quality float64) (*processed, error) {
	return processVariants(data, mime, quality, posterVariants)
}

func processVariants(data []byte, mime string, quality float64, specs []variantSpec) (*processed, error) {
	ext, ok := allowedMimes[mime]
	if !ok {
		return nil, ErrUnsupportedType
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("media: decoding image: %w", err)
	}
	bounds := img.Bounds()
	p := &processed{Width: bounds.Dx(), Height: bounds.Dy(), Ext: ext, VariantExt: ".webp"}

	for _, spec := range specs {
		v, err := encodeVariant(img, spec, p.VariantExt, quality)
		if err != nil {
			return nil, err
		}
		p.Variants = append(p.Variants, v)
	}
	return p, nil
}

// encodeVariant resizes img to one rung of the ladder and encodes it as
// WebP. The target size comes from variantSize and is passed to Resize in
// full, so the encoded object's dimensions are exactly the ones ImageFor
// will later report; an image at or below the bound is re-encoded as it is.
func encodeVariant(img image.Image, spec variantSpec, ext string, quality float64) (variant, error) {
	b := img.Bounds()
	w, h := variantSize(b.Dx(), b.Dy(), spec)
	out := img
	if w != b.Dx() || h != b.Dy() {
		out = imaging.Resize(img, w, h, imaging.Lanczos)
	}
	data, err := encodeWebP(out, quality)
	if err != nil {
		return variant{}, err
	}
	return variant{Name: spec.Name, Ext: ext, Mime: "image/webp", Width: w, Height: h, Data: data}, nil
}

func encodeWebP(img image.Image, quality float64) ([]byte, error) {
	if quality <= 0 || quality > 1 {
		quality = DefaultWebPQuality
	}
	var buf bytes.Buffer
	// The encoder's scale is 0–100.
	if err := webp.Encode(&buf, img, webp.Options{Quality: int(quality*100 + 0.5)}); err != nil {
		return nil, fmt.Errorf("media: encoding variant: %w", err)
	}
	return buf.Bytes(), nil
}
