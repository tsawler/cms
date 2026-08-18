package admin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tsawler/cms/media"
)

// The editor's picker gets the media library as JSON, and a template
// image slot chooses which of an item's URLs to store from it — so a
// rendition the picker is never sent is one no slot can ask for. The card
// rung is the one a slot smaller than the page asks for by name, through
// data-cms-rendition.
func TestMediaJSONCarriesTheCardRendition(t *testing.T) {
	image := toMediaJSON(media.View{
		Media:       media.Media{ID: 7, Kind: media.KindImage, Filename: "atv.jpg", Alt: "An ATV"},
		OriginalURL: "/cms/media/7/original.jpg",
		WebURL:      "/cms/media/7/web.webp",
		CardURL:     "/cms/media/7/card.webp",
		ThumbURL:    "/cms/media/7/thumb.webp",
	})
	if image.Card != "/cms/media/7/card.webp" {
		t.Errorf("Card = %q, want the card rendition's URL", image.Card)
	}
	body, err := json.Marshal(image)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if !strings.Contains(string(body), `"card":"/cms/media/7/card.webp"`) {
		t.Errorf("the picker is not sent a card URL:\n%s", body)
	}

	// Only images have one. A document or a video would send an empty
	// string, which reads as a URL that resolves to the page itself; the
	// field is omitted instead, and the picker falls back to the web
	// rendition every item does have.
	for _, tt := range []struct {
		name string
		view media.View
	}{
		{"a document", media.View{
			Media:       media.Media{ID: 8, Kind: media.KindFile, Filename: "brochure.pdf"},
			OriginalURL: "/cms/media/8/brochure.pdf",
			WebURL:      "/cms/media/8/brochure.pdf",
		}},
		{"a video", media.View{
			Media:       media.Media{ID: 9, Kind: media.KindVideo, Filename: "walkaround.mp4"},
			OriginalURL: "/cms/media/9/original.mp4",
			WebURL:      "/cms/media/9/original.mp4",
			PosterURL:   "/cms/media/9/web.webp",
		}},
	} {
		t.Run(tt.name+" has no card rendition", func(t *testing.T) {
			body, err := json.Marshal(toMediaJSON(tt.view))
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}
			if strings.Contains(string(body), `"card"`) {
				t.Errorf("%s sent a card URL:\n%s", tt.name, body)
			}
		})
	}
}
