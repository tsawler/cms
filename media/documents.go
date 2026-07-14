package media

import (
	"path"
	"regexp"
	"strings"
)

// docType describes one allowed document format: the Content-Type objects
// are stored and served with, and the sniffed types we accept for it
// (extensions are client-supplied, so the bytes must plausibly match).
type docType struct {
	ContentType   string
	SniffPrefixes []string
}

// docTypes is the whitelist of non-image uploads. Deliberately absent:
// HTML, SVG, XML, JS — anything a browser would execute — because media is
// served from the site's own origin when proxied.
var docTypes = map[string]docType{
	".pdf":  {"application/pdf", []string{"application/pdf"}},
	".doc":  {"application/msword", []string{"application/x-ole-storage", "application/octet-stream"}},
	".xls":  {"application/vnd.ms-excel", []string{"application/x-ole-storage", "application/octet-stream"}},
	".ppt":  {"application/vnd.ms-powerpoint", []string{"application/x-ole-storage", "application/octet-stream"}},
	".docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", []string{"application/zip"}},
	".xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", []string{"application/zip"}},
	".pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", []string{"application/zip"}},
	".odt":  {"application/vnd.oasis.opendocument.text", []string{"application/zip"}},
	".ods":  {"application/vnd.oasis.opendocument.spreadsheet", []string{"application/zip"}},
	".odp":  {"application/vnd.oasis.opendocument.presentation", []string{"application/zip"}},
	".txt":  {"text/plain; charset=utf-8", []string{"text/plain"}},
	".csv":  {"text/csv; charset=utf-8", []string{"text/plain"}},
	".zip":  {"application/zip", []string{"application/zip"}},
}

// docTypeFor validates a document upload by extension and sniffed content
// type, returning the extension and the Content-Type to store it under.
func docTypeFor(filename, sniffed string) (ext, contentType string, ok bool) {
	ext = strings.ToLower(path.Ext(filename))
	dt, found := docTypes[ext]
	if !found {
		return "", "", false
	}
	for _, prefix := range dt.SniffPrefixes {
		if strings.HasPrefix(sniffed, prefix) {
			return ext, dt.ContentType, true
		}
	}
	return "", "", false
}

var unsafeObjectChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// safeObjectName turns a client filename into a safe object name that
// still downloads with a recognizable filename, e.g.
// "Q3 Report (final).PDF" -> "q3-report-final.pdf".
func safeObjectName(filename, ext string) string {
	base := strings.TrimSuffix(path.Base(filename), path.Ext(filename))
	base = unsafeObjectChars.ReplaceAllString(strings.ToLower(base), "-")
	base = strings.Trim(base, "-.")
	if len(base) > 80 {
		base = base[:80]
	}
	if base == "" {
		base = "file"
	}
	return base + ext
}
