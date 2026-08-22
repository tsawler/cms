package admin

import "testing"

// The saved name comes from whatever someone uploaded, so the header has
// to survive quotes, non-ASCII, and a path that should never have been a
// filename. Browsers read filename* when it is there and the quoted
// fallback when it is not, so both have to be right.
func TestAttachmentDisposition(t *testing.T) {
	for _, tt := range []struct {
		name     string
		filename string
		want     string
	}{
		{"a plain name", "kraken.jpg", `attachment; filename="kraken.jpg"; filename*=UTF-8''kraken.jpg`},
		{"a quote in the name", `say "hi".png`, `attachment; filename="say _hi_.png"; filename*=UTF-8''say%20%22hi%22.png`},
		{"an accent", "résumé.pdf", `attachment; filename="r_sum_.pdf"; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf`},
		{"a path", "../../etc/passwd", `attachment; filename="passwd"; filename*=UTF-8''passwd`},
		{"nothing usable", "", `attachment; filename="upload"; filename*=UTF-8''upload`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := attachmentDisposition(tt.filename); got != tt.want {
				t.Errorf("attachmentDisposition(%q)\n got %s\nwant %s", tt.filename, got, tt.want)
			}
		})
	}
}
