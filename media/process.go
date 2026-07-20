package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/disintegration/imaging"
	"github.com/gen2brain/webp"
	_ "golang.org/x/image/webp" // decode-only
)

// ErrUnsupportedType is returned for uploads that are neither a supported
// image (JPEG, PNG, GIF, WebP) nor a whitelisted document type (PDF,
// office formats, text/CSV, ZIP).
var ErrUnsupportedType = errors.New("media: unsupported file type")

const (
	// webMaxWidth is the largest width served to pages; wider originals
	// are downscaled into the "web" variant.
	webMaxWidth = 1600
	// thumbSize bounds the admin media-library thumbnail.
	thumbSize = 320

	// DefaultWebPQuality is the lossy WebP quality, on a 0–1 scale, used
	// for the web and thumb variants unless the host configures another
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

// variant is one derived rendition of an upload.
type variant struct {
	Name string // "web" or "thumb"
	Ext  string // with dot
	Mime string
	Data []byte
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

	web := img
	if p.Width > webMaxWidth {
		web = imaging.Resize(img, webMaxWidth, 0, imaging.Lanczos)
	}
	webData, err := encodeWebP(web, quality)
	if err != nil {
		return nil, err
	}
	p.Variants = append(p.Variants, variant{Name: "web", Ext: p.VariantExt, Mime: "image/webp", Data: webData})

	thumb := imaging.Fit(img, thumbSize, thumbSize, imaging.Lanczos)
	thumbData, err := encodeWebP(thumb, quality)
	if err != nil {
		return nil, err
	}
	p.Variants = append(p.Variants, variant{Name: "thumb", Ext: p.VariantExt, Mime: "image/webp", Data: thumbData})

	return p, nil
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
