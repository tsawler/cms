package media

import (
	"errors"
	"path"
	"strings"
)

// ErrTooLarge is returned for uploads over the size cap for their kind:
// MaxImageDocBytes for images and documents, the Manager's video limit
// (SetMaxVideoBytes) for videos.
var ErrTooLarge = errors.New("media: file too large")

const (
	// MaxImageDocBytes caps image and document uploads, which are
	// buffered in memory for validation and processing.
	MaxImageDocBytes = 25 << 20

	// DefaultMaxVideoBytes caps video uploads unless the host configures
	// another limit (cms.Config.MediaMaxVideoMB). Videos are streamed to
	// the object store, so the cap guards storage, not memory.
	DefaultMaxVideoBytes = 512 << 20
)

// videoTypes is the whitelist of video uploads: only formats every modern
// browser plays natively, so a stored-as-uploaded file is always playable.
// Deliberately absent: MOV, AVI, MKV — accepting them would just produce
// videos most visitors' browsers can't play. Like the document whitelist,
// the client-supplied extension must be corroborated by the sniffed bytes;
// "application/octet-stream" is accepted for .mp4 because Go's sniffer only
// recognizes a subset of MP4 brand strings.
var videoTypes = map[string]docType{
	".mp4":  {"video/mp4", []string{"video/mp4", "application/octet-stream"}},
	".webm": {"video/webm", []string{"video/webm"}},
}

// videoTypeFor validates a video upload by extension and sniffed content
// type, returning the extension and the Content-Type to store it under.
func videoTypeFor(filename, sniffed string) (ext, contentType string, ok bool) {
	ext = strings.ToLower(path.Ext(filename))
	vt, found := videoTypes[ext]
	if !found {
		return "", "", false
	}
	for _, prefix := range vt.SniffPrefixes {
		if strings.HasPrefix(sniffed, prefix) {
			return ext, vt.ContentType, true
		}
	}
	return "", "", false
}
