package media

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// OpenOriginal is what the admin's download streams, so it has to hand
// back the bytes that were uploaded — not a rendition. An image is the
// case that can go wrong: it is the one kind whose original shares a
// prefix with a ladder of derived objects.
func TestOpenOriginalStreamsTheUploadedBytes(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		m, _ := newTestManager(db)

		png := testImage(t, 40, 20, "png")
		image, err := m.Upload(ctx, "photo.png", png, 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		doc := []byte("%PDF-1.4\nnot really a pdf\n")
		file, err := m.Upload(ctx, "brochure.pdf", doc, 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}

		for _, tt := range []struct {
			name string
			md   *Media
			want []byte
		}{
			{"an image", image, png},
			{"a document", file, doc},
		} {
			t.Run(tt.name, func(t *testing.T) {
				body, contentType, err := m.OpenOriginal(ctx, tt.md)
				if err != nil {
					t.Fatalf("OpenOriginal: %v", err)
				}
				defer body.Close()
				got, err := io.ReadAll(body)
				if err != nil {
					t.Fatalf("reading: %v", err)
				}
				if !bytes.Equal(got, tt.want) {
					t.Errorf("got %d bytes, want the %d uploaded", len(got), len(tt.want))
				}
				if contentType == "" {
					t.Error("no content type: the download would have to sniff")
				}
			})
		}
	})
}
