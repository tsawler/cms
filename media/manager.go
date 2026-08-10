package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/tsawler/cms/internal/sqldb"
)

// Kind distinguishes images (resized, embeddable) and videos (stored as
// uploaded, embedded as players) from plain files like PDFs and office
// documents (stored as-is, linked to).
type Kind string

const (
	KindImage Kind = "image"
	KindFile  Kind = "file"
	KindVideo Kind = "video"
)

// Media is one uploaded image, video, or document. Alt is the alt text for
// the locale it was loaded with (images only).
type Media struct {
	ID   int64
	Kind Kind
	// StoreKey locates the item's objects *relative to the media root*, so
	// it embeds neither "media/" nor the deployment's S3Config.KeyPrefix:
	// images and videos store the bare item id (their objects live at
	// StoreKey+"/original.<ext>" under the root), files store
	// "<id>/<name>.<ext>", the object itself. Manager.abs composes the
	// absolute key. Keeping the root out of the database is what lets a
	// deployment change its KeyPrefix, and what lets a bucket be adopted
	// into a deployment that uses a different one.
	StoreKey   string
	Filename   string
	Mime       string
	Ext        string
	VariantExt string // extension of the web/thumb objects (".webp"; legacy rows ".jpg"/".png"); empty for a video without a poster
	Width      int    // zero for files, and for videos without a poster
	Height     int    // zero for files, and for videos without a poster
	Size       int64
	FolderID   *int64 // nil = unfiled
	UploadedBy *int64
	CreatedAt  time.Time
	Alt        string
}

// FolderIDValue returns the folder id, or 0 when unfiled — convenient in
// templates, where pointers are awkward to compare.
func (md Media) FolderIDValue() int64 {
	if md.FolderID == nil {
		return 0
	}
	return *md.FolderID
}

// ItemID returns the opaque per-upload id every object of this item lives
// under — StoreKey for images and videos, its leading path segment for
// files. It names the item's manifest.
func (md Media) ItemID() string {
	if i := strings.IndexByte(md.StoreKey, '/'); i >= 0 {
		return md.StoreKey[:i]
	}
	return md.StoreKey
}

// ErrNotFound is returned when no media matches the query.
var ErrNotFound = errors.New("media: not found")

// ErrBadFilename is returned by Rename for a name that is empty (or all
// path separators) once sanitized, or longer than maxFilenameLen.
var ErrBadFilename = errors.New("media: bad filename")

// maxFilenameLen caps renamed filenames. The column is TEXT, so this
// guards the UI, not the database: a name this long already ellipsizes
// everywhere it is shown.
const maxFilenameLen = 200

// Manager coordinates the object store and the Postgres metadata.
type Manager struct {
	db            *sqldb.DB
	objects       ObjectStore
	keyRoot       string
	manifestRoot  string
	webpQuality   float64
	maxVideoBytes int64
	logger        *slog.Logger

	// Rebuilding renditions the ladder gained after an upload; see
	// regenerate.go.
	rebuilds  singleflight.Group
	rebuildOK chan struct{}
}

// NewManager returns a Manager storing binaries in objects and metadata in
// db. If objects implements KeyPrefixer, its prefix namespaces every key,
// letting several deployments share one bucket.
func NewManager(db *sqldb.DB, objects ObjectStore, logger *slog.Logger) *Manager {
	var prefix string
	if kp, ok := objects.(KeyPrefixer); ok {
		prefix = kp.KeyPrefix()
	}
	return &Manager{db: db, objects: objects,
		keyRoot: keyRoot(prefix), manifestRoot: manifestRoot(prefix),
		webpQuality: DefaultWebPQuality, maxVideoBytes: DefaultMaxVideoBytes, logger: logger,
		rebuildOK: make(chan struct{}, rebuildSlots())}
}

// SetWebPQuality overrides the lossy WebP quality used for image variants.
// Values outside (0, 1] are ignored.
func (m *Manager) SetWebPQuality(q float64) {
	if q > 0 && q <= 1 {
		m.webpQuality = q
	}
}

// SetMaxVideoBytes overrides the video upload size cap. Values below one
// are ignored.
func (m *Manager) SetMaxVideoBytes(n int64) {
	if n > 0 {
		m.maxVideoBytes = n
	}
}

// MaxVideoBytes returns the video upload size cap, for request-body limits
// and user-facing messages.
func (m *Manager) MaxVideoBytes() int64 {
	return m.maxVideoBytes
}

// KeyRoot returns the bucket prefix every object this manager stores lives
// under: "media/", or "<prefix>/media/" for a store with a deployment
// prefix.
func (m *Manager) KeyRoot() string {
	return m.keyRoot
}

// abs turns a Media.StoreKey-relative path into the absolute bucket key.
// Every call into the ObjectStore goes through it, which is what keeps the
// deployment prefix out of the database.
func (m *Manager) abs(relative string) string {
	return m.keyRoot + relative
}

// Upload validates and stores a buffered upload, returning its record. It
// is UploadFrom for callers that already hold the bytes; videos should
// arrive through UploadFrom so they stream to the object store instead.
func (m *Manager) Upload(ctx context.Context, filename string, data []byte, uploadedBy int64, folderID *int64) (*Media, error) {
	return m.UploadFrom(ctx, filename, bytes.NewReader(data), int64(len(data)), nil, uploadedBy, folderID)
}

// UploadFrom validates and stores an upload, sniffing its leading bytes to
// pick the pipeline. Images are buffered and stored untouched alongside
// resized WebP web and thumb variants; SVGs (selected by extension —
// sniffing can't identify them) are validated as script-free and stored
// as their own variants, since vectors need no resizing; whitelisted
// documents (PDF, office formats, text/CSV, ZIP) are buffered and stored
// as-is; videos (MP4, WebM) are streamed to the object store as uploaded
// — no transcoding.
// size is the upload's byte length (multipart.FileHeader.Size). poster
// optionally carries a client-captured still image for videos, processed
// into the same web/thumb variants images get; it is ignored for other
// kinds and dropped, not fatal, when undecodable. A non-nil folderID files
// the upload into that folder.
func (m *Manager) UploadFrom(ctx context.Context, filename string, src io.ReadSeeker, size int64, poster []byte, uploadedBy int64, folderID *int64) (*Media, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	sniffed := http.DetectContentType(head[:n])

	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	// The item id, and so every key below, is relative to the media root.
	itemID := hex.EncodeToString(buf)

	stored := []string{}
	cleanup := func() {
		for _, key := range stored {
			if err := m.objects.Delete(context.WithoutCancel(ctx), m.abs(key)); err != nil {
				m.logger.Error("cms media: cleaning up after failed upload", "key", key, "err", err)
			}
		}
	}
	// put takes a root-relative key, matching what lands in the database.
	put := func(key, contentType string, body io.Reader) error {
		if err := m.objects.Put(ctx, m.abs(key), contentType, body); err != nil {
			cleanup()
			return err
		}
		stored = append(stored, key)
		return nil
	}

	md := &Media{
		Filename: filename,
		Size:     size,
		FolderID: folderID,
	}
	if uploadedBy != 0 {
		md.UploadedBy = &uploadedBy
	}

	if ext, contentType, ok := videoTypeFor(filename, sniffed); ok {
		if size > m.maxVideoBytes {
			return nil, ErrTooLarge
		}
		md.Kind = KindVideo
		md.StoreKey = itemID
		md.Mime = contentType
		md.Ext = ext

		// The poster is decorative; a bad one downgrades the video to
		// posterless rather than failing the upload.
		if len(poster) > 0 {
			p, err := processPoster(poster, http.DetectContentType(poster), m.webpQuality)
			if err != nil {
				m.logger.Warn("cms media: dropping undecodable video poster", "filename", filename, "err", err)
			} else {
				md.VariantExt = p.VariantExt
				md.Width, md.Height = p.Width, p.Height
				for _, v := range p.Variants {
					if err := put(itemID+"/"+v.Name+v.Ext, v.Mime, bytes.NewReader(v.Data)); err != nil {
						return nil, err
					}
				}
			}
		}
		if err := put(itemID+"/original"+ext, contentType, src); err != nil {
			return nil, err
		}
	} else if isSVGFilename(filename) {
		// SVG never sniffs as image/*; the extension selects the
		// pipeline and processSVG requires a real, script-free SVG.
		if size > MaxImageDocBytes {
			return nil, ErrTooLarge
		}
		data, err := io.ReadAll(src)
		if err != nil {
			return nil, err
		}
		p, err := processSVG(data)
		if err != nil {
			return nil, err
		}
		md.Kind = KindImage
		md.StoreKey = itemID
		md.Mime = svgContentType
		md.Ext = p.Ext
		md.VariantExt = p.VariantExt
		md.Width, md.Height = p.Width, p.Height

		if err := put(itemID+"/original"+p.Ext, svgContentType, bytes.NewReader(data)); err != nil {
			return nil, err
		}
		for _, v := range p.Variants {
			if err := put(itemID+"/"+v.Name+v.Ext, v.Mime, bytes.NewReader(v.Data)); err != nil {
				return nil, err
			}
		}
	} else if strings.HasPrefix(sniffed, "image/") {
		if size > MaxImageDocBytes {
			return nil, ErrTooLarge
		}
		data, err := io.ReadAll(src)
		if err != nil {
			return nil, err
		}
		p, err := process(data, sniffed, m.webpQuality)
		if err != nil {
			return nil, err
		}
		md.Kind = KindImage
		md.StoreKey = itemID
		md.Mime = sniffed
		md.Ext = p.Ext
		md.VariantExt = p.VariantExt
		md.Width, md.Height = p.Width, p.Height

		if err := put(itemID+"/original"+p.Ext, sniffed, bytes.NewReader(data)); err != nil {
			return nil, err
		}
		for _, v := range p.Variants {
			if err := put(itemID+"/"+v.Name+v.Ext, v.Mime, bytes.NewReader(v.Data)); err != nil {
				return nil, err
			}
		}
	} else {
		ext, contentType, ok := docTypeFor(filename, sniffed)
		if !ok {
			return nil, ErrUnsupportedType
		}
		if size > MaxImageDocBytes {
			return nil, ErrTooLarge
		}
		data, err := io.ReadAll(src)
		if err != nil {
			return nil, err
		}
		md.Kind = KindFile
		md.StoreKey = itemID + "/" + safeObjectName(filename, ext)
		md.Mime = contentType
		md.Ext = ext

		if err := put(md.StoreKey, contentType, bytes.NewReader(data)); err != nil {
			return nil, err
		}
	}

	// Folders hold one kind only. An upload aimed at a folder of another
	// kind — e.g. a PDF uploaded while browsing an image folder, which
	// preselects it — lands unfiled instead, where it stays visible. A
	// folder deleted mid-upload is handled the same way.
	if md.FolderID != nil {
		var folderKind Kind
		err := m.db.QueryRow(ctx,
			"SELECT kind FROM cms_media_folders WHERE id = $1", *md.FolderID).Scan(&folderKind)
		if err != nil || folderKind != md.Kind {
			md.FolderID = nil
		}
	}

	// created_at is set here rather than read back from a RETURNING clause,
	// which MySQL does not have. The column keeps its NOT NULL default for
	// any row written outside this path.
	md.CreatedAt = time.Now().UTC()
	md.ID, err = m.db.InsertID(ctx, `
		INSERT INTO cms_media (kind, store_key, filename, mime, ext, variant_ext, width, height, size, folder_id, uploaded_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		md.Kind, md.StoreKey, md.Filename, md.Mime, md.Ext, md.VariantExt, md.Width, md.Height, md.Size, md.FolderID, md.UploadedBy, md.CreatedAt)
	if err != nil {
		cleanup()
		return nil, err
	}

	// Written last: a crash before this leaves objects nobody references,
	// which is invisible and cheap to sweep, where a manifest without its
	// objects would be a broken item on the next restore.
	m.syncManifest(ctx, md.ID)
	return md, nil
}

const mediaColumns = `m.id, m.kind, m.store_key, m.filename, m.mime, m.ext, m.variant_ext, m.width, m.height, m.size,
	m.folder_id, m.uploaded_by, m.created_at, COALESCE(t.alt_text, '')`

func scanMedia(row sqldb.Scanner) (*Media, error) {
	var md Media
	err := row.Scan(&md.ID, &md.Kind, &md.StoreKey, &md.Filename, &md.Mime, &md.Ext, &md.VariantExt, &md.Width, &md.Height,
		&md.Size, &md.FolderID, &md.UploadedBy, &md.CreatedAt, &md.Alt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &md, nil
}

// GetByID returns one media record with alt text for locale.
func (m *Manager) GetByID(ctx context.Context, id int64, locale string) (*Media, error) {
	return scanMedia(m.db.QueryRow(ctx, `
		SELECT `+mediaColumns+`
		FROM cms_media m
		LEFT JOIN cms_media_meta t ON t.media_id = m.id AND t.locale = $2
		WHERE m.id = $1`, id, locale))
}

// byStoreKey returns the record whose objects live under key, without alt
// text. The media proxy uses it to identify the item a requested object
// belongs to.
func (m *Manager) byStoreKey(ctx context.Context, key string) (*Media, error) {
	return scanMedia(m.db.QueryRow(ctx, `
		SELECT `+mediaColumns+`
		FROM cms_media m
		LEFT JOIN cms_media_meta t ON t.media_id = m.id AND t.locale = $2
		WHERE m.store_key = $1`, key, ""))
}

// ListOptions filters All. The zero value lists everything.
type ListOptions struct {
	Kind     Kind   // "" = both images and files
	Query    string // case-insensitive substring match on filename
	FolderID *int64 // only items in this folder
	Unfiled  bool   // only items in no folder (ignored when FolderID set)
}

var ilikeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// All returns media records with alt text for locale, newest first,
// filtered by opts.
func (m *Manager) All(ctx context.Context, locale string, opts ListOptions) ([]Media, error) {
	q := `
		SELECT ` + mediaColumns + `
		FROM cms_media m
		LEFT JOIN cms_media_meta t ON t.media_id = m.id AND t.locale = $1`
	args := []any{locale}
	where := []string{}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if opts.Kind != "" {
		where = append(where, "m.kind = "+arg(opts.Kind))
	}
	if opts.Query != "" {
		// Postgres needs ILIKE; MySQL's default collations already compare
		// case-insensitively, so the dialect picks the spelling.
		where = append(where, m.db.Dialect().CaseInsensitiveLike(
			"m.filename", arg("%"+ilikeEscaper.Replace(opts.Query)+"%")))
	}
	switch {
	case opts.FolderID != nil:
		where = append(where, "m.folder_id = "+arg(*opts.FolderID))
	case opts.Unfiled:
		where = append(where, "m.folder_id IS NULL")
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += ` ORDER BY m.created_at DESC, m.id DESC`
	rows, err := m.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (Media, error) {
		md, err := scanMedia(row)
		if err != nil {
			return Media{}, err
		}
		return *md, nil
	})
}

// Count returns how many media items exist across every kind — the number
// the admin shows beside its Media nav entry.
func (m *Manager) Count(ctx context.Context) (int, error) {
	var n int
	err := m.db.QueryRow(ctx, "SELECT count(*) FROM cms_media").Scan(&n)
	return n, err
}

// SetPoster attaches a poster frame to an existing video, processing the
// still into the same web/thumb variants an upload-time poster gets and
// updating the record's variant metadata. The admin uses it to backfill
// videos uploaded without one — the server can't decode video, so the
// frame is captured in a browser. Existing poster objects are overwritten
// in place: the variant keys are deterministic, so a failure part-way
// leaves nothing a retry won't replace. Non-video media is refused.
func (m *Manager) SetPoster(ctx context.Context, id int64, locale string, poster []byte) (*Media, error) {
	md, err := m.GetByID(ctx, id, locale)
	if err != nil {
		return nil, err
	}
	if md.Kind != KindVideo {
		return nil, ErrUnsupportedType
	}
	p, err := processPoster(poster, http.DetectContentType(poster), m.webpQuality)
	if err != nil {
		return nil, err
	}
	for _, v := range p.Variants {
		if err := m.objects.Put(ctx, m.abs(md.StoreKey+"/"+v.Name+v.Ext), v.Mime, bytes.NewReader(v.Data)); err != nil {
			return nil, err
		}
	}
	md.VariantExt = p.VariantExt
	md.Width, md.Height = p.Width, p.Height
	_, err = m.db.Exec(ctx, `
		UPDATE cms_media SET variant_ext = $1, width = $2, height = $3 WHERE id = $4`,
		md.VariantExt, md.Width, md.Height, md.ID)
	if err != nil {
		return nil, err
	}
	m.syncManifest(ctx, md.ID)
	return md, nil
}

// Rename changes a media item's display name. Purely a metadata change:
// objects live under an opaque item id (or, for documents, a key cut from
// the upload-time name), and none of them move — which is what keeps
// every existing link to the item working. The stored extension is
// reattached whatever was submitted, so a rename can never make the name
// contradict the bytes; a trailing copy of it in the new name is absorbed
// rather than doubled. The extension alone is not a name, so a rename to
// just ".pdf" comes back ErrBadFilename.
func (m *Manager) Rename(ctx context.Context, id int64, locale, name string) (*Media, error) {
	md, err := m.GetByID(ctx, id, locale)
	if err != nil {
		return nil, err
	}

	// Keep the final path element, defensively, like uploads do.
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if md.Ext != "" {
		if stem, ok := cutSuffixFold(name, md.Ext); ok {
			name = strings.TrimSpace(stem)
		}
		name += md.Ext
	}
	if name == "" || name == md.Ext || len(name) > maxFilenameLen {
		return nil, ErrBadFilename
	}

	md.Filename = name
	if _, err := m.db.Exec(ctx,
		"UPDATE cms_media SET filename = $1 WHERE id = $2", name, id); err != nil {
		return nil, err
	}
	m.syncManifest(ctx, id)
	return md, nil
}

// cutSuffixFold is strings.CutSuffix, matching case-insensitively —
// ".MP4" is the same extension as ".mp4".
func cutSuffixFold(s, suffix string) (string, bool) {
	if len(s) < len(suffix) || !strings.EqualFold(s[len(s)-len(suffix):], suffix) {
		return s, false
	}
	return s[:len(s)-len(suffix)], true
}

// UpdateAlt sets the alt text for one media record and locale.
func (m *Manager) UpdateAlt(ctx context.Context, id int64, locale, alt string) error {
	_, err := m.db.Exec(ctx, `
		INSERT INTO cms_media_meta (media_id, locale, alt_text)
		VALUES ($1, $2, $3)
		ON CONFLICT (media_id, locale) DO UPDATE SET alt_text = EXCLUDED.alt_text`,
		id, locale, alt)
	if err != nil {
		return err
	}
	m.syncManifest(ctx, id)
	return nil
}

// Delete removes a media record, its manifest, and its bucket objects.
// Pages that still reference the image keep its (now dead) URL; the admin UI
// warns about this before deleting.
func (m *Manager) Delete(ctx context.Context, id int64, locale string) error {
	md, err := m.GetByID(ctx, id, locale)
	if err != nil {
		return err
	}
	// The manifest goes first, and unlike the binaries a failure here stops
	// the delete: a manifest that outlives its row would resurrect the item
	// on the next restore, which is worse than a delete the user retries.
	if err := m.deleteManifest(ctx, md.ItemID()); err != nil {
		return err
	}
	for _, key := range m.objectKeys(md) {
		if err := m.objects.Delete(ctx, key); err != nil {
			m.logger.Error("cms media: deleting object", "key", key, "err", err)
		}
	}
	_, err = m.db.Exec(ctx, "DELETE FROM cms_media WHERE id = $1", id)
	return err
}

// variants returns the rendition ladder md's derived objects follow —
// the full image ladder, or the shorter one a video's poster frame gets.
func (md Media) variants() []variantSpec {
	if md.Kind == KindVideo {
		return posterVariants
	}
	return imageVariants
}

// objectKeys returns the absolute bucket keys of every object belonging to
// md, ready to hand to the ObjectStore.
func (m *Manager) objectKeys(md *Media) []string {
	if md.Kind == KindFile {
		return []string{m.abs(md.StoreKey)}
	}
	keys := []string{m.abs(md.StoreKey + "/original" + md.Ext)}
	if md.VariantExt != "" {
		for _, spec := range md.variants() {
			keys = append(keys, m.abs(md.StoreKey+"/"+spec.Name+md.VariantExt))
		}
	}
	return keys
}

// URL returns the public URL of one rendition: "original" or any rung of
// the ladder ("web", "card", "thumb"). Files have a single rendition,
// returned for any name. Videos have "original" (the video itself; also
// returned for "web"), plus "poster" and "thumb" — empty when the video
// has no poster.
func (m *Manager) URL(md *Media, rendition string) string {
	original := func() string {
		return m.objects.PublicURL(m.abs(md.StoreKey + "/original" + md.Ext))
	}
	switch md.Kind {
	case KindFile:
		return m.objects.PublicURL(m.abs(md.StoreKey))
	case KindVideo:
		switch rendition {
		case "poster", "thumb":
			if md.VariantExt == "" {
				return ""
			}
			// The poster's renditions reuse the image variant names, so
			// the full-size poster lives at web.<ext>.
			name := "web"
			if rendition == "thumb" {
				name = "thumb"
			}
			return m.objects.PublicURL(m.abs(md.StoreKey + "/" + name + md.VariantExt))
		default:
			return original()
		}
	}
	if _, ok := variantSpecFor(rendition); !ok || md.VariantExt == "" {
		return original()
	}
	return m.objects.PublicURL(m.abs(md.StoreKey + "/" + rendition + md.VariantExt))
}

// View is a Media with its URLs precomputed, for templates.
type View struct {
	Media
	OriginalURL string
	WebURL      string
	CardURL     string // images only: the listing-card rendition
	ThumbURL    string
	PosterURL   string // videos only: the full-size poster frame, if one exists
}

// Views converts records to template-ready views. Files have no thumbnail;
// their web and original URLs are the document itself. Videos' web and
// original URLs are both the video; poster and thumb are empty without a
// poster frame.
func (m *Manager) Views(items []Media) []View {
	out := make([]View, len(items))
	for i, md := range items {
		v := View{Media: md}
		switch md.Kind {
		case KindFile:
			url := m.URL(&md, "original")
			v.OriginalURL, v.WebURL = url, url
		case KindVideo:
			url := m.URL(&md, "original")
			v.OriginalURL, v.WebURL = url, url
			v.PosterURL = m.URL(&md, "poster")
			v.ThumbURL = m.URL(&md, "thumb")
		default:
			v.OriginalURL = m.URL(&md, "original")
			v.WebURL = m.URL(&md, "web")
			v.CardURL = m.URL(&md, "card")
			v.ThumbURL = m.URL(&md, "thumb")
		}
		out[i] = v
	}
	return out
}

// Rendition is one size of an image, as a srcset candidate.
type Rendition struct {
	URL    string
	Width  int
	Height int
}

// Image is one uploaded image ready to put in an <img> element: a default
// src sized for the slot it fills, the srcset of every distinct rendition
// so the browser can pick a better one, and the intrinsic size of that
// default so it can reserve the space before the bytes arrive.
//
// Templates use it as
//
//	<img src="{{.URL}}" srcset="{{.Srcset}}" sizes="..."
//	     width="{{.Width}}" height="{{.Height}}" alt="{{.Alt}}">
//
// with sizes written by the template, since only it knows the layout.
// Every field is safe to use on its own: a legacy or external image with
// no library record still carries a URL, just no srcset or dimensions.
type Image struct {
	URL        string
	Srcset     string
	Width      int
	Height     int
	Alt        string
	Renditions []Rendition
}

// ImageFor builds the <img> data for one library image. prefer names the
// rung used as the default src — "web" for a full-width slot, "card" for a
// listing card, "thumb" for a small preview; an unknown name falls back to
// "web".
//
// The srcset lists every rung, smaller ones included, so a card slot can
// still take the thumbnail on a narrow phone. Rungs that would come out the
// same width are listed once: an image narrower than the ladder's bounds
// is not upscaled, so its larger rungs are all the same picture.
//
// Vectors get a single URL and no srcset — an SVG scales losslessly, so
// alternate widths would be the same bytes under different names. A nil or
// non-image record returns nil, which templates render as nothing.
func (m *Manager) ImageFor(md *Media, prefer string) *Image {
	if md == nil || md.Kind != KindImage {
		return nil
	}
	img := &Image{Alt: md.Alt, Width: md.Width, Height: md.Height}
	if md.Ext == ".svg" {
		img.URL = m.URL(md, "web")
		return img
	}
	if _, ok := variantSpecFor(prefer); !ok {
		prefer = "web"
	}
	img.URL = m.URL(md, prefer)

	// Dimensions are unknown (a record written before they were recorded,
	// say): serve the preferred rendition and let the browser measure it.
	if md.Width <= 0 || md.Height <= 0 {
		img.Width, img.Height = 0, 0
		return img
	}

	seen := map[int]bool{}
	for _, spec := range md.variants() {
		w, h := variantSize(md.Width, md.Height, spec)
		if spec.Name == prefer {
			img.Width, img.Height = w, h
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		img.Renditions = append(img.Renditions, Rendition{URL: m.URL(md, spec.Name), Width: w, Height: h})
	}
	// A srcset with one candidate tells the browser nothing it does not
	// already have from src.
	if len(img.Renditions) > 1 {
		var sb strings.Builder
		for i, r := range img.Renditions {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(r.URL)
			sb.WriteString(" ")
			sb.WriteString(strconv.Itoa(r.Width))
			sb.WriteString("w")
		}
		img.Srcset = sb.String()
	}
	return img
}

// SizeHuman renders Size for people, e.g. "1.4 MB".
func (md Media) SizeHuman() string {
	switch {
	case md.Size >= 1<<20:
		return strconv.FormatFloat(float64(md.Size)/(1<<20), 'f', 1, 64) + " MB"
	case md.Size >= 1<<10:
		return strconv.FormatInt(md.Size/(1<<10), 10) + " KB"
	default:
		return strconv.FormatInt(md.Size, 10) + " B"
	}
}
