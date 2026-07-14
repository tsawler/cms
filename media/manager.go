package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Media is one uploaded image. Alt is the alt text for the locale it was
// loaded with.
type Media struct {
	ID         int64
	S3Key      string // key prefix; objects live at S3Key/original.<ext> etc.
	Filename   string
	Mime       string
	Ext        string
	Width      int
	Height     int
	Size       int64
	UploadedBy *int64
	CreatedAt  time.Time
	Alt        string
}

// ErrNotFound is returned when no media matches the query.
var ErrNotFound = errors.New("media: not found")

// Manager coordinates the object store and the Postgres metadata.
type Manager struct {
	db      *pgxpool.Pool
	objects ObjectStore
	logger  *slog.Logger
}

// NewManager returns a Manager storing binaries in objects and metadata in
// db.
func NewManager(db *pgxpool.Pool, objects ObjectStore, logger *slog.Logger) *Manager {
	return &Manager{db: db, objects: objects, logger: logger}
}

// Upload validates, processes, and stores an image, returning its record.
// The original is stored untouched alongside resized web and thumb
// variants.
func (m *Manager) Upload(ctx context.Context, filename string, data []byte, uploadedBy int64) (*Media, error) {
	mime := http.DetectContentType(data)
	p, err := process(data, mime)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	prefix := "media/" + hex.EncodeToString(buf)

	stored := []string{}
	cleanup := func() {
		for _, key := range stored {
			if err := m.objects.Delete(context.WithoutCancel(ctx), key); err != nil {
				m.logger.Error("cms media: cleaning up after failed upload", "key", key, "err", err)
			}
		}
	}

	put := func(key, contentType string, body []byte) error {
		if err := m.objects.Put(ctx, key, contentType, bytes.NewReader(body)); err != nil {
			return err
		}
		stored = append(stored, key)
		return nil
	}

	if err := put(prefix+"/original"+p.Ext, mime, data); err != nil {
		return nil, err
	}
	for _, v := range p.Variants {
		if err := put(prefix+"/"+v.Name+v.Ext, v.Mime, v.Data); err != nil {
			cleanup()
			return nil, err
		}
	}

	md := &Media{
		S3Key:    prefix,
		Filename: filename,
		Mime:     mime,
		Ext:      p.Ext,
		Width:    p.Width,
		Height:   p.Height,
		Size:     int64(len(data)),
	}
	if uploadedBy != 0 {
		md.UploadedBy = &uploadedBy
	}
	err = m.db.QueryRow(ctx, `
		INSERT INTO cms_media (s3_key, filename, mime, ext, width, height, size, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`,
		md.S3Key, md.Filename, md.Mime, md.Ext, md.Width, md.Height, md.Size, md.UploadedBy,
	).Scan(&md.ID, &md.CreatedAt)
	if err != nil {
		cleanup()
		return nil, err
	}
	return md, nil
}

const mediaColumns = `m.id, m.s3_key, m.filename, m.mime, m.ext, m.width, m.height, m.size,
	m.uploaded_by, m.created_at, COALESCE(t.alt_text, '')`

func scanMedia(row pgx.Row) (*Media, error) {
	var md Media
	err := row.Scan(&md.ID, &md.S3Key, &md.Filename, &md.Mime, &md.Ext, &md.Width, &md.Height,
		&md.Size, &md.UploadedBy, &md.CreatedAt, &md.Alt)
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

// All returns every media record with alt text for locale, newest first.
func (m *Manager) All(ctx context.Context, locale string) ([]Media, error) {
	rows, err := m.db.Query(ctx, `
		SELECT `+mediaColumns+`
		FROM cms_media m
		LEFT JOIN cms_media_meta t ON t.media_id = m.id AND t.locale = $1
		ORDER BY m.created_at DESC, m.id DESC`, locale)
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
	variantExt := ".jpg"
	if md.Mime == "image/png" {
		variantExt = ".png"
	}
	return []string{
		md.S3Key + "/original" + md.Ext,
		md.S3Key + "/web" + variantExt,
		md.S3Key + "/thumb" + variantExt,
	}
}

// URL returns the public URL of one rendition: "original", "web", or
// "thumb".
func (m *Manager) URL(md *Media, rendition string) string {
	variantExt := ".jpg"
	if md.Mime == "image/png" {
		variantExt = ".png"
	}
	switch rendition {
	case "web":
		return m.objects.PublicURL(md.S3Key + "/web" + variantExt)
	case "thumb":
		return m.objects.PublicURL(md.S3Key + "/thumb" + variantExt)
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
}

// Views converts records to template-ready views.
func (m *Manager) Views(items []Media) []View {
	out := make([]View, len(items))
	for i, md := range items {
		out[i] = View{
			Media:       md,
			OriginalURL: m.URL(&md, "original"),
			WebURL:      m.URL(&md, "web"),
			ThumbURL:    m.URL(&md, "thumb"),
		}
	}
	return out
}
