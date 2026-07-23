package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	ID         int64
	Kind       Kind
	S3Key      string // images/videos: key prefix (objects at S3Key/original.<ext> etc.); files: the full object key
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

// ErrNotFound is returned when no media matches the query.
var ErrNotFound = errors.New("media: not found")

// Manager coordinates the object store and the Postgres metadata.
type Manager struct {
	db            *pgxpool.Pool
	objects       ObjectStore
	keyRoot       string
	webpQuality   float64
	maxVideoBytes int64
	logger        *slog.Logger
}

// NewManager returns a Manager storing binaries in objects and metadata in
// db. If objects implements KeyPrefixer, its prefix namespaces every key,
// letting several deployments share one bucket.
func NewManager(db *pgxpool.Pool, objects ObjectStore, logger *slog.Logger) *Manager {
	var prefix string
	if kp, ok := objects.(KeyPrefixer); ok {
		prefix = kp.KeyPrefix()
	}
	return &Manager{db: db, objects: objects, keyRoot: keyRoot(prefix),
		webpQuality: DefaultWebPQuality, maxVideoBytes: DefaultMaxVideoBytes, logger: logger}
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
	prefix := m.keyRoot + hex.EncodeToString(buf)

	stored := []string{}
	cleanup := func() {
		for _, key := range stored {
			if err := m.objects.Delete(context.WithoutCancel(ctx), key); err != nil {
				m.logger.Error("cms media: cleaning up after failed upload", "key", key, "err", err)
			}
		}
	}
	put := func(key, contentType string, body io.Reader) error {
		if err := m.objects.Put(ctx, key, contentType, body); err != nil {
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
		md.S3Key = prefix
		md.Mime = contentType
		md.Ext = ext

		// The poster is decorative; a bad one downgrades the video to
		// posterless rather than failing the upload.
		if len(poster) > 0 {
			p, err := process(poster, http.DetectContentType(poster), m.webpQuality)
			if err != nil {
				m.logger.Warn("cms media: dropping undecodable video poster", "filename", filename, "err", err)
			} else {
				md.VariantExt = p.VariantExt
				md.Width, md.Height = p.Width, p.Height
				for _, v := range p.Variants {
					if err := put(prefix+"/"+v.Name+v.Ext, v.Mime, bytes.NewReader(v.Data)); err != nil {
						return nil, err
					}
				}
			}
		}
		if err := put(prefix+"/original"+ext, contentType, src); err != nil {
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
		md.S3Key = prefix
		md.Mime = svgContentType
		md.Ext = p.Ext
		md.VariantExt = p.VariantExt
		md.Width, md.Height = p.Width, p.Height

		if err := put(prefix+"/original"+p.Ext, svgContentType, bytes.NewReader(data)); err != nil {
			return nil, err
		}
		for _, v := range p.Variants {
			if err := put(prefix+"/"+v.Name+v.Ext, v.Mime, bytes.NewReader(v.Data)); err != nil {
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
		md.S3Key = prefix
		md.Mime = sniffed
		md.Ext = p.Ext
		md.VariantExt = p.VariantExt
		md.Width, md.Height = p.Width, p.Height

		if err := put(prefix+"/original"+p.Ext, sniffed, bytes.NewReader(data)); err != nil {
			return nil, err
		}
		for _, v := range p.Variants {
			if err := put(prefix+"/"+v.Name+v.Ext, v.Mime, bytes.NewReader(v.Data)); err != nil {
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
		md.S3Key = prefix + "/" + safeObjectName(filename, ext)
		md.Mime = contentType
		md.Ext = ext

		if err := put(md.S3Key, contentType, bytes.NewReader(data)); err != nil {
			return nil, err
		}
	}

	err = m.db.QueryRow(ctx, `
		INSERT INTO cms_media (kind, s3_key, filename, mime, ext, variant_ext, width, height, size, folder_id, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at`,
		md.Kind, md.S3Key, md.Filename, md.Mime, md.Ext, md.VariantExt, md.Width, md.Height, md.Size, md.FolderID, md.UploadedBy,
	).Scan(&md.ID, &md.CreatedAt)
	if err != nil {
		cleanup()
		return nil, err
	}
	return md, nil
}

const mediaColumns = `m.id, m.kind, m.s3_key, m.filename, m.mime, m.ext, m.variant_ext, m.width, m.height, m.size,
	m.folder_id, m.uploaded_by, m.created_at, COALESCE(t.alt_text, '')`

func scanMedia(row pgx.Row) (*Media, error) {
	var md Media
	err := row.Scan(&md.ID, &md.Kind, &md.S3Key, &md.Filename, &md.Mime, &md.Ext, &md.VariantExt, &md.Width, &md.Height,
		&md.Size, &md.FolderID, &md.UploadedBy, &md.CreatedAt, &md.Alt)
	if errors.Is(err, pgx.ErrNoRows) {
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
		where = append(where, "m.filename ILIKE "+arg("%"+ilikeEscaper.Replace(opts.Query)+"%"))
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
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Media, error) {
		md, err := scanMedia(row)
		if err != nil {
			return Media{}, err
		}
		return *md, nil
	})
}

// UpdateAlt sets the alt text for one media record and locale.
func (m *Manager) UpdateAlt(ctx context.Context, id int64, locale, alt string) error {
	_, err := m.db.Exec(ctx, `
		INSERT INTO cms_media_meta (media_id, locale, alt_text)
		VALUES ($1, $2, $3)
		ON CONFLICT (media_id, locale) DO UPDATE SET alt_text = EXCLUDED.alt_text`,
		id, locale, alt)
	return err
}

// Delete removes a media record and its bucket objects. Pages that still
// reference the image keep its (now dead) URL; the admin UI warns about
// this before deleting.
func (m *Manager) Delete(ctx context.Context, id int64, locale string) error {
	md, err := m.GetByID(ctx, id, locale)
	if err != nil {
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

func (m *Manager) objectKeys(md *Media) []string {
	if md.Kind == KindFile {
		return []string{md.S3Key}
	}
	keys := []string{md.S3Key + "/original" + md.Ext}
	if md.VariantExt != "" {
		keys = append(keys,
			md.S3Key+"/web"+md.VariantExt,
			md.S3Key+"/thumb"+md.VariantExt)
	}
	return keys
}

// URL returns the public URL of one rendition: "original", "web", or
// "thumb". Files have a single rendition, returned for any name. Videos
// have "original" (the video itself; also returned for "web"), plus
// "poster" and "thumb" — empty when the video has no poster.
func (m *Manager) URL(md *Media, rendition string) string {
	switch md.Kind {
	case KindFile:
		return m.objects.PublicURL(md.S3Key)
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
			return m.objects.PublicURL(md.S3Key + "/" + name + md.VariantExt)
		default:
			return m.objects.PublicURL(md.S3Key + "/original" + md.Ext)
		}
	}
	switch rendition {
	case "web":
		return m.objects.PublicURL(md.S3Key + "/web" + md.VariantExt)
	case "thumb":
		return m.objects.PublicURL(md.S3Key + "/thumb" + md.VariantExt)
	default:
		return m.objects.PublicURL(md.S3Key + "/original" + md.Ext)
	}
}

// View is a Media with its URLs precomputed, for templates.
type View struct {
	Media
	OriginalURL string
	WebURL      string
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
			v.ThumbURL = m.URL(&md, "thumb")
		}
		out[i] = v
	}
	return out
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
