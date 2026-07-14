package media

import "testing"

func TestDocTypeFor(t *testing.T) {
	cases := []struct {
		filename, sniffed string
		wantOK            bool
		wantType          string
	}{
		{"report.pdf", "application/pdf", true, "application/pdf"},
		{"Report.PDF", "application/pdf", true, "application/pdf"},
		{"sheet.xlsx", "application/zip", true, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"deck.pptx", "application/zip", true, "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"legacy.doc", "application/x-ole-storage", true, "application/msword"},
		{"notes.txt", "text/plain; charset=utf-8", true, "text/plain; charset=utf-8"},
		{"data.csv", "text/plain; charset=utf-8", true, "text/csv; charset=utf-8"},
		// Extension/content mismatches and dangerous types must fail.
		{"fake.pdf", "text/html; charset=utf-8", false, ""},
		{"page.html", "text/html; charset=utf-8", false, ""},
		{"vector.svg", "image/svg+xml", false, ""},
		{"script.js", "text/plain; charset=utf-8", false, ""},
		{"binary.exe", "application/octet-stream", false, ""},
		{"noext", "application/pdf", false, ""},
	}
	for _, c := range cases {
		_, contentType, ok := docTypeFor(c.filename, c.sniffed)
		if ok != c.wantOK {
			t.Errorf("docTypeFor(%q, %q) ok = %v, want %v", c.filename, c.sniffed, ok, c.wantOK)
			continue
		}
		if ok && contentType != c.wantType {
			t.Errorf("docTypeFor(%q) type = %q, want %q", c.filename, contentType, c.wantType)
		}
	}
}

func TestSafeObjectName(t *testing.T) {
	cases := map[string]string{
		"Q3 Report (final).PDF": "q3-report-final.pdf",
		"résumé.pdf":            "r-sum.pdf",
		"...pdf":                "file.pdf",
		"weird/../path.pdf":     "path.pdf",
	}
	for in, want := range cases {
		if got := safeObjectName(in, ".pdf"); got != want {
			t.Errorf("safeObjectName(%q) = %q, want %q", in, got, want)
		}
	}
}
