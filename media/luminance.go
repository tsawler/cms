package media

import (
	"context"
	"errors"
	"image"
)

// ErrNoRaster is returned when an image's lightness cannot be measured
// because there are no pixels to measure: a vector, or a record whose
// derived renditions were never made.
var ErrNoRaster = errors.New("media: no raster rendition to measure")

// darkThreshold is the average perceived brightness, on the 0–255 scale
// below, under which an image counts as dark. It matches the editor's own
// isDarkColor (editor/src/sections.js), so a picture and a flat colour
// are judged the same way.
const darkThreshold = 140

// luminanceStride keeps the measurement cheap on the largest rendition a
// caller might pass: every 4th pixel in each direction is 1/16th of the
// work and makes no practical difference to an average.
const luminanceStride = 4

// IsDark reports whether an image reads as dark overall — the question to
// ask before laying text over one, since the answer decides whether that
// text should be light or dark.
//
// It measures the thumbnail rendition: the smallest object of the set, so
// the answer costs one small fetch and a decode of a few hundred pixels
// square. Vectors have no rendition to measure and return ErrNoRaster;
// callers that only want a hint can treat any error as "not dark", which
// leaves text in the site's own colour.
func (m *Manager) IsDark(ctx context.Context, md *Media) (bool, error) {
	if md == nil || md.Kind != KindImage || md.VariantExt == "" {
		return false, ErrNoRaster
	}
	body, _, err := m.objects.Get(ctx, m.abs(md.StoreKey+"/thumb"+md.VariantExt))
	if err != nil {
		return false, err
	}
	defer body.Close()

	img, _, err := image.Decode(body)
	if err != nil {
		return false, err
	}
	return averageBrightness(img) < darkThreshold, nil
}

// averageBrightness is the mean perceived brightness of an image's pixels
// on a 0–255 scale, weighting the channels the way an eye does. Fully
// transparent pixels are skipped: they show whatever is behind them, so
// counting them as black would call a transparent PNG dark.
func averageBrightness(img image.Image) float64 {
	b := img.Bounds()
	var total float64
	var n int
	for y := b.Min.Y; y < b.Max.Y; y += luminanceStride {
		for x := b.Min.X; x < b.Max.X; x += luminanceStride {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			// RGBA() returns alpha-premultiplied 16-bit values; undo both
			// to get the colour as it will be seen against a backdrop.
			r = r * 0xffff / a
			g = g * 0xffff / a
			bl = bl * 0xffff / a
			total += 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(bl>>8)
			n++
		}
	}
	if n == 0 {
		return 255 // nothing opaque to judge: treat as light
	}
	return total / float64(n)
}
