package media

import (
	"image"
	"image/color"
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
