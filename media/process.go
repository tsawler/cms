package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"

	"github.com/disintegration/imaging"
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

	jpegQuality = 85
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
	Width    int
	Height   int
	Ext      string // original's extension, with dot
	Variants []variant
}

// process validates and decodes an uploaded image and produces its resized
// variants. PNG variants stay PNG (preserving transparency); JPEG, GIF, and
// WebP variants are encoded as JPEG.
func process(data []byte, mime string) (*processed, error) {
	ext, ok := allowedMimes[mime]
	if !ok {
		return nil, ErrUnsupportedType
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("media: decoding image: %w", err)
	}
	bounds := img.Bounds()
	p := &processed{Width: bounds.Dx(), Height: bounds.Dy(), Ext: ext}

	variantExt, variantMime := ".jpg", "image/jpeg"
	if mime == "image/png" {
		variantExt, variantMime = ".png", "image/png"
	}

	web := img
	if p.Width > webMaxWidth {
		web = imaging.Resize(img, webMaxWidth, 0, imaging.Lanczos)
	}
	webData, err := encode(web, variantMime)
	if err != nil {
		return nil, err
	}
	p.Variants = append(p.Variants, variant{Name: "web", Ext: variantExt, Mime: variantMime, Data: webData})

	thumb := imaging.Fit(img, thumbSize, thumbSize, imaging.Lanczos)
	thumbData, err := encode(thumb, variantMime)
	if err != nil {
		return nil, err
	}
	p.Variants = append(p.Variants, variant{Name: "thumb", Ext: variantExt, Mime: variantMime, Data: thumbData})

	return p, nil
}

func encode(img image.Image, mime string) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	switch mime {
	case "image/png":
		err = png.Encode(&buf, img)
	default:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality})
	}
	if err != nil {
		return nil, fmt.Errorf("media: encoding variant: %w", err)
	}
	return buf.Bytes(), nil
}
