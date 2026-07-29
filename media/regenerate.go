package media

// Rebuilding a rendition on demand. The image ladder (process.go) grows
// over time — a release adds a rung, and every image uploaded before it has
// only the objects its own upload produced. Rewriting a whole bucket in a
// migration would be slow, would need the bucket reachable at deploy time,
// and would re-encode images nobody looks at.
//
// Instead the media proxy asks for the missing object here, on the first
// request that needs it: decode the item's stored original, encode the one
// rendition asked for, put it back. Every later request is an ordinary hit
// on an immutable, hard-cached object. That also repairs a rendition lost
// to a half-finished bucket copy, and lets Restore adopt an item whose
// derived objects never made it across.
//
// Only derived renditions are rebuildable. The original is the source, so
// its loss is real data loss, and a video's poster came from the browser
// at upload time and was never stored whole.

import (
	"bytes"
	"context"
	"errors"
	"image"
	"path"
	"runtime"
	"strings"
)

// ErrNoRendition is returned when a requested object is not a rendition
// this Manager can rebuild: an unknown name, an item that is not in the
// library, or a video poster, which has no stored source. It means "this
// object is legitimately absent", so the proxy answers 404 quietly rather
// than logging a failure.
var ErrNoRendition = errors.New("media: not a rebuildable rendition")

// rebuildSlots bounds how many renditions are re-encoded at once. Decoding
// and Lanczos-resizing a large image is the most CPU-hungry thing this
// package does, and a cold cache under traffic can ask for many at once;
// the cap keeps that from crowding out request serving. Rebuilds are rare
// and one-off, so a small share of the machine is enough.
func rebuildSlots() int {
	return min(max(runtime.NumCPU()/2, 1), 4)
}

// EnsureRendition builds the derived object at rest, a media-root-relative
// key like "9f2c…/card.webp", and stores it. It returns nil once the object
// exists, so the caller can go straight back to the object store for it.
//
// Concurrent requests for the same object share one rebuild, and rebuilds
// across the process share a small pool of workers, so a listing page full
// of images that all need the same new rung costs one encode each rather
// than one per visitor.
func (m *Manager) EnsureRendition(ctx context.Context, rest string) error {
	itemID, file, ok := strings.Cut(rest, "/")
	if !ok || itemID == "" || strings.Contains(file, "/") {
		return ErrNoRendition
	}
	spec, ok := variantSpecFor(strings.TrimSuffix(file, path.Ext(file)))
	if !ok {
		return ErrNoRendition
	}
	_, err, _ := m.rebuilds.Do(rest, func() (any, error) {
		return nil, m.rebuild(ctx, itemID, file, spec)
	})
	return err
}

// rebuild does the work of one EnsureRendition, once the key has been
// parsed into an item and a rung of the ladder.
func (m *Manager) rebuild(ctx context.Context, itemID, file string, spec variantSpec) error {
	md, err := m.byStoreKey(ctx, itemID)
	if errors.Is(err, ErrNotFound) {
		return ErrNoRendition
	}
	if err != nil {
		return err
	}
	// The requested name has to be the rendition this item would actually
	// have: right kind, right extension. Anything else is a URL for an
	// object that never existed.
	if md.Kind != KindImage || md.VariantExt == "" || file != spec.Name+md.VariantExt {
		return ErrNoRendition
	}

	select {
	case m.rebuildOK <- struct{}{}:
		defer func() { <-m.rebuildOK }()
	case <-ctx.Done():
		return ctx.Err()
	}

	source, err := m.getObject(ctx, m.abs(md.StoreKey+"/original"+md.Ext))
	if err != nil {
		return err
	}

	key := m.abs(md.StoreKey + "/" + file)
	// A vector's renditions are all the original bytes.
	if md.Ext == ".svg" {
		return m.objects.Put(ctx, key, svgContentType, bytes.NewReader(source))
	}
	img, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return err
	}
	v, err := encodeVariant(img, spec, md.VariantExt, m.webpQuality)
	if err != nil {
		return err
	}
	m.logger.Info("cms media: rebuilt a missing rendition", "key", key, "size", len(v.Data))
	return m.objects.Put(ctx, key, v.Mime, bytes.NewReader(v.Data))
}
