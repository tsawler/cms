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
// as KindImage only after checkSVGToken rejects every scripting vector:
// script/foreignObject/handler elements, on* and XML Events attributes,
// javascript: and non-image data: URLs — including data:image/svg+xml,
// which says image and means document — SMIL animations that rewrite a
// link target or animate a dangerous URL into one, DTD internal subsets
// that could define entity bombs, and non-xml processing instructions.
//
// The media proxy serves image/svg+xml with a script-blocking
// Content-Security-Policy as defense in depth. Note that this second
// layer is the CMS's own response header, so it covers proxied media
// only: a deployment serving directly from a public bucket or CDN
// (S3Config.PublicRead, PublicBaseURL) has this scan and nothing else.
//
// Being vector graphics they need no raster variants — the same bytes
// are stored under every rendition name, so pickers and pages use them
// exactly like any other image.

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
	p := &processed{
		Width:      width,
		Height:     height,
		Ext:        ".svg",
		VariantExt: ".svg",
	}
	// One rung per raster rendition, all the same bytes, so an SVG answers
	// every URL the ladder can produce and pickers and pages use it exactly
	// like any other image.
	for _, spec := range imageVariants {
		p.Variants = append(p.Variants, variant{
			Name: spec.Name, Ext: ".svg", Mime: svgContentType,
			Width: width, Height: height, Data: data,
		})
	}
	return p, nil
}

// scriptableElements carry executable content in their own right.
//
//	script        the obvious one
//	foreignObject embeds arbitrary HTML, <script> included
//	handler       XML Events' handler element, whose body is script text.
//	              Browsers dropped XML Events years ago, so this is not a
//	              live vector so much as one byte of the scan that costs
//	              nothing and would be embarrassing to be missing.
var scriptableElements = map[string]bool{
	"script": true, "foreignobject": true, "handler": true,
}

// animationElements are SMIL. They carry no URL of their own: they write
// a value into some *other* attribute while the image is on screen. That
// is what makes them worth naming here — see svgValueAttrs.
var animationElements = map[string]bool{
	"animate": true, "set": true, "animatetransform": true, "animatemotion": true,
}

// svgValueAttrs are the attributes whose value can end up being resolved
// as a URL.
//
// "href" covers xlink:href: Go reports the local name for both.
//
// The rest are the SMIL animation values, and they are the non-obvious
// half of this. An animation element looks inert — nothing about
// <set to="javascript:alert(1)"> is a URL — right up until it runs and
// assigns that string to whatever attributeName names. Put it inside an
// <a> targeting href and the link becomes a javascript: URL a moment
// after the image loads. So the same scheme check that guards href has
// to guard the values an animation can write into one.
var svgValueAttrs = map[string]bool{
	"href": true, "to": true, "from": true, "by": true, "values": true,
}

// checkSVGToken rejects an element that can execute or embed active
// content. name is the element's lowercased local name.
func checkSVGToken(name string, attrs []xml.Attr) error {
	if scriptableElements[name] {
		return ErrUnsafeSVG
	}
	for _, a := range attrs {
		an := strings.ToLower(a.Name.Local)
		switch {
		// Every SVG event attribute is on* (onload, onclick, onbegin, …)
		// and no legitimate presentation attribute shares the prefix.
		case strings.HasPrefix(an, "on"):
			return ErrUnsafeSVG

		// XML Events again: ev:event and ev:handler wire a listener onto
		// an ordinary element, so blocking the <handler> element alone
		// would leave the other half of the mechanism open.
		case an == "event" || an == "handler":
			return ErrUnsafeSVG

		// An animation that rewrites a link target has no legitimate use
		// in stored artwork, and refusing the whole family by what it
		// aims at is a smaller rule than chasing every way the value
		// could be spelled. The value check below is the second half of
		// this, deliberately overlapping.
		case an == "attributename" && animationElements[name]:
			attr := strings.ToLower(strings.TrimSpace(a.Value))
			if localName(attr) == "href" {
				return ErrUnsafeSVG
			}

		case svgValueAttrs[an]:
			// A SMIL value attribute is a ";"-separated list of the
			// values to step through; href is a single URL. Splitting
			// either way is safe — a data:image/png;base64,… href splits
			// into pieces that are still not a scheme.
			for v := range strings.SplitSeq(a.Value, ";") {
				if dangerousSVGURL(v) {
					return ErrUnsafeSVG
				}
			}
		}
	}
	return nil
}

// dangerousSVGURL reports whether v is a URL a browser would execute, or
// would treat as a document rather than as a picture.
func dangerousSVGURL(v string) bool {
	// Browsers tolerate embedded whitespace in URL schemes
	// ("java\nscript:"), so strip it before matching.
	v = strings.ToLower(strings.Map(func(r rune) rune {
		if r <= ' ' {
			return -1
		}
		return r
	}, v))
	switch {
	case strings.HasPrefix(v, "javascript:"), strings.HasPrefix(v, "vbscript:"):
		return true
	case !strings.HasPrefix(v, "data:"):
		return false
	// An inline image is fine to point at — except an inline SVG, which
	// is not a picture but another document, and one this scan is not
	// looking inside. "data:image/" would otherwise wave it through on
	// the strength of a MIME type that says image and means document.
	case strings.HasPrefix(v, "data:image/svg"):
		return true
	default:
		return !strings.HasPrefix(v, "data:image/")
	}
}

// localName drops an XML namespace prefix: "xlink:href" is href, and so
// is "href". Used on attribute *values* that name another attribute,
// where the decoder has done no splitting for us.
func localName(s string) string {
	if _, after, found := strings.Cut(s, ":"); found {
		return after
	}
	return s
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
