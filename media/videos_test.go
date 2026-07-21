package media

import "testing"

func TestVideoTypeFor(t *testing.T) {
	cases := []struct {
		filename, sniffed string
		wantOK            bool
		wantType          string
	}{
		{"clip.mp4", "video/mp4", true, "video/mp4"},
		{"Clip.MP4", "video/mp4", true, "video/mp4"},
		// Go's sniffer misses some MP4 brand strings; the extension plus
		// an unrecognized payload is accepted.
		{"clip.mp4", "application/octet-stream", true, "video/mp4"},
		{"clip.webm", "video/webm", true, "video/webm"},
		// Extension/content mismatches and unplayable formats must fail.
		{"clip.webm", "application/octet-stream", false, ""},
		{"fake.mp4", "application/zip", false, ""},
		{"fake.mp4", "text/html; charset=utf-8", false, ""},
		{"photo.mp4", "image/jpeg", false, ""},
		{"movie.mov", "video/quicktime", false, ""},
		{"movie.avi", "video/avi", false, ""},
		{"clip.mkv", "video/webm", false, ""},
		{"noext", "video/mp4", false, ""},
	}
	for _, c := range cases {
		_, contentType, ok := videoTypeFor(c.filename, c.sniffed)
		if ok != c.wantOK {
			t.Errorf("videoTypeFor(%q, %q) ok = %v, want %v", c.filename, c.sniffed, ok, c.wantOK)
			continue
		}
		if ok && contentType != c.wantType {
			t.Errorf("videoTypeFor(%q) type = %q, want %q", c.filename, contentType, c.wantType)
		}
	}
}
