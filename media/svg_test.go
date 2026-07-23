package media

import (
	"errors"
	"testing"
)

const cleanSVG = `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="24px" height="16" viewBox="0 0 24 16">
  <style>.a{fill:#c00}</style>
  <path class="a" d="M1 1h22v14H1z"/>
  <use href="#nothing"/>
</svg>`

func TestProcessSVGClean(t *testing.T) {
	p, err := processSVG([]byte(cleanSVG))
	if err != nil {
		t.Fatalf("processSVG: %v", err)
	}
	if p.Width != 24 || p.Height != 16 {
		t.Errorf("dimensions = %dx%d, want 24x16", p.Width, p.Height)
	}
	if p.Ext != ".svg" || p.VariantExt != ".svg" {
		t.Errorf("ext = %q / %q, want .svg / .svg", p.Ext, p.VariantExt)
	}
	if len(p.Variants) != 2 {
		t.Fatalf("got %d variants, want web and thumb", len(p.Variants))
	}
	for _, v := range p.Variants {
		if string(v.Data) != cleanSVG {
			t.Errorf("variant %s bytes differ from the original", v.Name)
		}
		if v.Mime != "image/svg+xml" {
			t.Errorf("variant %s mime = %q", v.Name, v.Mime)
		}
	}
}

func TestProcessSVGViewBoxFallback(t *testing.T) {
	p, err := processSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0,0,100,50"><path d="M0 0"/></svg>`))
	if err != nil {
		t.Fatalf("processSVG: %v", err)
	}
	if p.Width != 100 || p.Height != 50 {
		t.Errorf("dimensions = %dx%d, want 100x50", p.Width, p.Height)
	}
}

func TestProcessSVGRejectsActiveContent(t *testing.T) {
	cases := map[string]string{
		"script element":    `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"event handler":     `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><path d="M0 0"/></svg>`,
		"nested handler":    `<svg xmlns="http://www.w3.org/2000/svg"><circle r="1" onclick="alert(1)"/></svg>`,
		"foreignObject":     `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><body xmlns="http://www.w3.org/1999/xhtml"></body></foreignObject></svg>`,
		"javascript href":   `<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"><text>x</text></a></svg>`,
		"sneaky href":       "<svg xmlns=\"http://www.w3.org/2000/svg\"><a href=\"java\nscript:alert(1)\"><text>x</text></a></svg>",
		"xlink js href":     `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><a xlink:href="javascript:alert(1)"><text>x</text></a></svg>`,
		"data html href":    `<svg xmlns="http://www.w3.org/2000/svg"><a href="data:text/html;base64,PHNjcmlwdD4="><text>x</text></a></svg>`,
		"dtd subset":        `<!DOCTYPE svg [<!ENTITY x "y">]><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"xml-stylesheet pi": `<?xml-stylesheet href="http://evil.example/x.xsl" type="text/xsl"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
	}
	for name, src := range cases {
		if _, err := processSVG([]byte(src)); !errors.Is(err, ErrUnsafeSVG) {
			t.Errorf("%s: err = %v, want ErrUnsafeSVG", name, err)
		}
	}
}

func TestProcessSVGAllowsPlainDoctypeAndDataImage(t *testing.T) {
	ok := []string{
		`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd"><svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><image href="data:image/png;base64,iVBORw0KGgo="/></svg>`,
	}
	for _, src := range ok {
		if _, err := processSVG([]byte(src)); err != nil {
			t.Errorf("processSVG(%.40s…): %v", src, err)
		}
	}
}

func TestProcessSVGRejectsNonSVG(t *testing.T) {
	for name, src := range map[string]string{
		"html root": `<html><body>hi</body></html>`,
		"empty":     ``,
	} {
		if _, err := processSVG([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := processSVG([]byte(`<html><body>hi</body></html>`)); !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("html root: err = %v, want ErrUnsupportedType", err)
	}
	if _, err := processSVG([]byte{0xff, 0xd8, 0xff}); err == nil {
		t.Error("jpeg bytes: expected an error")
	}
}

func TestIsSVGFilename(t *testing.T) {
	if !isSVGFilename("logo.SVG") || !isSVGFilename("a/b/logo.svg") {
		t.Error("svg filenames not recognized")
	}
	if isSVGFilename("logo.png") || isSVGFilename("svg") {
		t.Error("non-svg filename recognized")
	}
}
