package media

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// SVG uploads are images to editors but documents to browsers: viewed
// inline, an SVG can run scripts on the site's origin. They are accepted
// as KindImage only after checkSVGToken rejects every scripting vector
// (script/foreignObject elements, on* event attributes, javascript: and
// non-image data: hrefs, DTD internal subsets that could define entity
// bombs, non-xml processing instructions), and the media proxy serves
// image/svg+xml with a script-blocking Content-Security-Policy as
// defense in depth. Being vector graphics they need no raster variants —
// the same bytes are stored as original, web, and thumb, so pickers and
// pages use them exactly like any other image.

const svgContentType = "image/svg+xml"

// ErrUnsafeSVG is returned for an SVG upload containing scripts or other
// active content the scan won't allow.
var ErrUnsafeSVG = errors.New("media: svg contains active content")

// isSVGFilename reports whether the upload claims to be an SVG. Content
// sniffing can't identify SVG (Go deliberately sniffs it as text), so the
// extension selects the pipeline and processSVG then requires an actual
// <svg> document.
func isSVGFilename(filename string) bool {
	return strings.EqualFold(path.Ext(filename), ".svg")
}

// processSVG validates an SVG upload and produces its variants — the
// original bytes under every rendition name, since SVG scales losslessly.
// Dimensions come from the root's width/height or viewBox when present.
func processSVG(data []byte) (*processed, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	// Non-strict parsing tolerates the unknown entities tools like
	// Illustrator emit; the Directive check below still rejects the DTD
	// subsets that could define them dangerously.
	d.Strict = false
	rootSeen := false
	var width, height int
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("media: parsing svg: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			// A DTD internal subset can define entities (XML bombs, XXE);
			// a plain <!DOCTYPE svg PUBLIC ...> carries none and passes.
			if bytes.ContainsRune(t, '[') {
				return nil, ErrUnsafeSVG
			}
		case xml.ProcInst:
			// Only the <?xml ...?> declaration is allowed;
			// <?xml-stylesheet?> can pull external resources.
			if t.Target != "xml" {
				return nil, ErrUnsafeSVG
			}
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if !rootSeen {
				if name != "svg" {
					return nil, ErrUnsupportedType
				}
				rootSeen = true
				width, height = svgDimensions(t)
			}
			if err := checkSVGToken(name, t.Attr); err != nil {
				return nil, err
			}
		}
	}
	if !rootSeen {
		return nil, ErrUnsupportedType
	}
	return &processed{
		Width:      width,
		Height:     height,
		Ext:        ".svg",
		VariantExt: ".svg",
		Variants: []variant{
			{Name: "web", Ext: ".svg", Mime: svgContentType, Data: data},
			{Name: "thumb", Ext: ".svg", Mime: svgContentType, Data: data},
		},
	}, nil
}

// checkSVGToken rejects an element that can execute or embed active
// content. name is the element's lowercased local name.
func checkSVGToken(name string, attrs []xml.Attr) error {
	// foreignObject embeds arbitrary HTML — script tags included.
	if name == "script" || name == "foreignobject" {
		return ErrUnsafeSVG
	}
	for _, a := range attrs {
		an := strings.ToLower(a.Name.Local)
		// Every SVG event attribute is on* (onload, onclick, onbegin, …)
		// and no legitimate presentation attribute shares the prefix.
		if strings.HasPrefix(an, "on") {
			return ErrUnsafeSVG
		}
		if an == "href" { // covers xlink:href too (same local name)
			// Browsers tolerate embedded whitespace in URL schemes
			// ("java\nscript:"), so strip it before matching.
			v := strings.ToLower(strings.Map(func(r rune) rune {
				if r <= ' ' {
					return -1
				}
				return r
			}, a.Value))
			if strings.HasPrefix(v, "javascript:") || strings.HasPrefix(v, "vbscript:") ||
				(strings.HasPrefix(v, "data:") && !strings.HasPrefix(v, "data:image/")) {
				return ErrUnsafeSVG
			}
		}
	}
	return nil
}

// svgDimensions reads the root element's pixel size, falling back to the
// viewBox. Zero means unknown — harmless, like a posterless video.
func svgDimensions(root xml.StartElement) (w, h int) {
	attrs := map[string]string{}
	for _, a := range root.Attr {
		attrs[strings.ToLower(a.Name.Local)] = a.Value
	}
	w, h = svgLength(attrs["width"]), svgLength(attrs["height"])
	if w > 0 && h > 0 {
		return w, h
	}
	// viewBox is "min-x min-y width height", comma- or space-separated.
	if f := strings.Fields(strings.ReplaceAll(attrs["viewbox"], ",", " ")); len(f) == 4 {
		vw, errW := strconv.ParseFloat(f[2], 64)
		vh, errH := strconv.ParseFloat(f[3], 64)
		if errW == nil && errH == nil && vw > 0 && vh > 0 {
			return int(vw + 0.5), int(vh + 0.5)
		}
	}
	return 0, 0
}

// svgLength parses a plain or px-suffixed CSS length to a rounded int;
// percentages and other units return 0 (unknown).
func svgLength(s string) int {
	s = strings.TrimSuffix(strings.TrimSpace(s), "px")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int(f + 0.5)
}
