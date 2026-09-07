package media

// S3Store against a real S3-compatible server. Everything else in the
// package can be tested against an in-memory stand-in, but this type is
// nothing but its conversation with a bucket: what it sends, what it does
// with what comes back, and which of the many ways a bucket can say "no"
// it turns into ErrObjectNotFound or ErrInvalidRange rather than a bare
// error. A stub store would only restate the code.
//
// MinIO stands in for the bucket, reached over plain HTTP through the
// real NewS3Store. The container is started lazily, once per test process
// and shared by every test here, the way dbtest shares its databases;
// without Docker these skip, so `go test ./...` still passes.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tsawler/cms/internal/dbtest"
)

const (
	minioUser = "cmsroot"
	minioPass = "cmsrootsecret"
)

var (
	minioOnce     sync.Once
	minioEndpoint string // "host:port", no scheme
	minioErr      error
)

// startMinIO brings up one MinIO for the whole package and returns its
// endpoint.
func startMinIO(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("media: skipping the S3 container test in -short mode")
	}
	dbtest.SkipWithoutDocker(t)

	minioOnce.Do(func() {
		ctx := context.Background()
		container, err := testcontainers.Run(ctx, "minio/minio:RELEASE.2025-04-22T22-12-26Z",
			testcontainers.WithExposedPorts("9000/tcp"),
			testcontainers.WithEnv(map[string]string{
				"MINIO_ROOT_USER":     minioUser,
				"MINIO_ROOT_PASSWORD": minioPass,
			}),
			testcontainers.WithCmd("server", "/data"),
			testcontainers.WithWaitStrategy(
				wait.ForHTTP("/minio/health/live").
					WithPort("9000/tcp").
					WithStartupTimeout(90*time.Second),
			),
		)
		if err != nil {
			minioErr = fmt.Errorf("starting container: %w", err)
			return
		}
		host, err := container.Host(ctx)
		if err != nil {
			minioErr = fmt.Errorf("container host: %w", err)
			return
		}
		port, err := container.MappedPort(ctx, "9000/tcp")
		if err != nil {
			minioErr = fmt.Errorf("mapped port: %w", err)
			return
		}
		minioEndpoint = host + ":" + port.Port()
	})
	if minioErr != nil {
		t.Fatalf("media: starting MinIO: %v", minioErr)
	}
	return minioEndpoint
}

// newS3TestStore returns an S3Store on a bucket of its own, so tests never
// see each other's objects. It goes through the real NewS3Store: the
// container speaks plain HTTP, which is what the http:// endpoint is for.
func newS3TestStore(t *testing.T, cfg S3Config) *S3Store {
	t.Helper()
	endpoint := startMinIO(t)

	cfg.Endpoint = "http://" + endpoint
	cfg.AccessKey, cfg.Secret = minioUser, minioPass
	cfg.UsePathStyle = true // MinIO addresses buckets by path
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.Bucket == "" {
		cfg.Bucket = "cms-" + strings.ToLower(strings.NewReplacer(
			"/", "-", "_", "-", " ", "-").Replace(t.Name()))
	}

	store, err := NewS3Store(cfg)
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	if _, err := store.client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(cfg.Bucket),
	}); err != nil {
		t.Fatalf("creating bucket %s: %v", cfg.Bucket, err)
	}
	return store
}

// put stores an object and fails the test if it does not land.
func put(t *testing.T, s *S3Store, key, contentType, body string) {
	t.Helper()
	if err := s.Put(context.Background(), key, contentType, strings.NewReader(body)); err != nil {
		t.Fatalf("Put(%s): %v", key, err)
	}
}

// read drains a body and closes it.
func read(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return string(data)
}

func TestS3PutAndGet(t *testing.T) {
	s := newS3TestStore(t, S3Config{})
	ctx := context.Background()

	put(t, s, "media/abc/original.png", "image/png", "the bytes")

	body, contentType, err := s.Get(ctx, "media/abc/original.png")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := read(t, body); got != "the bytes" {
		t.Errorf("body = %q, want %q", got, "the bytes")
	}
	// The content type is what the media proxy hands the browser, so it
	// has to survive the round trip rather than come back as the
	// bucket's default.
	if contentType != "image/png" {
		t.Errorf("content type = %q, want image/png", contentType)
	}
}

// Keys are unique per upload, so an object never changes and is cached
// hard — the header rides with the object, for deployments that serve
// direct bucket URLs and never touch the CMS's proxy.
func TestS3PutSetsImmutableCaching(t *testing.T) {
	s := newS3TestStore(t, S3Config{})
	ctx := context.Background()
	put(t, s, "media/abc/original.png", "image/png", "the bytes")

	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String("media/abc/original.png"),
	})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if cc := aws.ToString(head.CacheControl); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
}

// Put overwrites rather than failing, which is what makes a re-run of a
// rendition rebuild harmless.
func TestS3PutOverwrites(t *testing.T) {
	s := newS3TestStore(t, S3Config{})
	ctx := context.Background()

	put(t, s, "media/abc/web.webp", "image/webp", "first")
	put(t, s, "media/abc/web.webp", "image/webp", "second")

	body, _, err := s.Get(ctx, "media/abc/web.webp")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := read(t, body); got != "second" {
		t.Errorf("body = %q, want the second write", got)
	}
}

// A missing key is the one error the callers branch on: the media proxy
// turns it into a rendition rebuild or a 404, and anything else into a
// 500. Getting this mapping wrong makes a missing thumbnail a server
// error on every page that shows it.
func TestS3GetMissingKey(t *testing.T) {
	s := newS3TestStore(t, S3Config{})

	_, _, err := s.Get(context.Background(), "media/nothing/here.png")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("err = %v, want ErrObjectNotFound", err)
	}
}

// GetRange is what makes proxied video seekable, and playable at all in
// Safari, which probes with a Range request before it will start.
func TestS3GetRange(t *testing.T) {
	s := newS3TestStore(t, S3Config{})
	ctx := context.Background()
	const full = "0123456789abcdef"
	put(t, s, "media/abc/original.mp4", "video/mp4", full)

	t.Run("a bounded range", func(t *testing.T) {
		body, contentType, contentRange, length, err := s.GetRange(ctx, "media/abc/original.mp4", "bytes=4-7")
		if err != nil {
			t.Fatalf("GetRange: %v", err)
		}
		if got := read(t, body); got != "4567" {
			t.Errorf("body = %q, want %q", got, "4567")
		}
		// The player needs the size of what it got and where it sits in
		// the whole file; a length describing the file instead would
		// truncate playback.
		if length != 4 {
			t.Errorf("length = %d, want 4", length)
		}
		if want := fmt.Sprintf("bytes 4-7/%d", len(full)); contentRange != want {
			t.Errorf("content range = %q, want %q", contentRange, want)
		}
		if contentType != "video/mp4" {
			t.Errorf("content type = %q, want video/mp4", contentType)
		}
	})

	t.Run("an open-ended range", func(t *testing.T) {
		body, _, contentRange, length, err := s.GetRange(ctx, "media/abc/original.mp4", "bytes=10-")
		if err != nil {
			t.Fatalf("GetRange: %v", err)
		}
		if got := read(t, body); got != full[10:] {
			t.Errorf("body = %q, want %q", got, full[10:])
		}
		if length != int64(len(full)-10) {
			t.Errorf("length = %d, want %d", length, len(full)-10)
		}
		if want := fmt.Sprintf("bytes 10-%d/%d", len(full)-1, len(full)); contentRange != want {
			t.Errorf("content range = %q, want %q", contentRange, want)
		}
	})

	// Past the end is the client's mistake, and the proxy answers it with
	// 416 — which it can only do if this is told apart from a real
	// failure.
	t.Run("a range past the end", func(t *testing.T) {
		_, _, _, _, err := s.GetRange(ctx, "media/abc/original.mp4", "bytes=9999-")
		if !errors.Is(err, ErrInvalidRange) {
			t.Errorf("err = %v, want ErrInvalidRange", err)
		}
	})

	t.Run("a missing key", func(t *testing.T) {
		_, _, _, _, err := s.GetRange(ctx, "media/nothing/here.mp4", "bytes=0-3")
		if !errors.Is(err, ErrObjectNotFound) {
			t.Errorf("err = %v, want ErrObjectNotFound", err)
		}
	})

	// A range the store cannot honor comes back as the whole object with
	// no Content-Range, which the proxy passes through as a 200. What
	// matters is that it is not mistaken for a partial response.
	t.Run("an unparseable range", func(t *testing.T) {
		body, _, contentRange, _, err := s.GetRange(ctx, "media/abc/original.mp4", "kilometres=0-3")
		if err != nil {
			t.Fatalf("GetRange: %v", err)
		}
		if got := read(t, body); got != full {
			t.Errorf("body = %q, want the whole object", got)
		}
		if contentRange != "" {
			t.Errorf("content range = %q, want none for a whole-object response", contentRange)
		}
	})
}

func TestS3Delete(t *testing.T) {
	s := newS3TestStore(t, S3Config{})
	ctx := context.Background()
	put(t, s, "media/abc/original.png", "image/png", "the bytes")

	if err := s.Delete(ctx, "media/abc/original.png"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := s.Get(ctx, "media/abc/original.png"); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("after Delete, Get = %v, want ErrObjectNotFound", err)
	}

	// Deleting a key that is not there is documented not to be an error,
	// which is what lets Manager.Delete sweep a rendition ladder without
	// knowing which rungs an old upload actually has.
	if err := s.Delete(ctx, "media/abc/original.png"); err != nil {
		t.Errorf("deleting a missing key: %v, want nil", err)
	}
	if err := s.Delete(ctx, "media/never/existed.png"); err != nil {
		t.Errorf("deleting a key that never existed: %v, want nil", err)
	}
}

func TestS3List(t *testing.T) {
	s := newS3TestStore(t, S3Config{})
	ctx := context.Background()
	put(t, s, "media/abc/original.png", "image/png", "one")
	put(t, s, "media/abc/thumb.webp", "image/webp", "two")
	put(t, s, "media/def/original.png", "image/png", "three")
	put(t, s, "manifests/abc.json", "application/json", "{}")

	got, err := s.List(ctx, "media/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	keys := map[string]ObjectInfo{}
	for _, o := range got {
		keys[o.Key] = o
	}
	if len(keys) != 3 {
		t.Fatalf("List(media/) returned %d objects, want 3: %v", len(keys), got)
	}
	// The manifests live outside the media root and name uploaders, which
	// is why a media listing must not reach them.
	if _, ok := keys["manifests/abc.json"]; ok {
		t.Error("a media listing reached the manifests")
	}
	// Size and modification time are what Restore adopts a bucket by.
	if info := keys["media/abc/original.png"]; info.Size != 3 {
		t.Errorf("size = %d, want 3", info.Size)
	}
	if info := keys["media/abc/original.png"]; info.LastModified.IsZero() {
		t.Error("no modification time, which is what an adoption orders by")
	}

	// A narrower prefix narrows the listing.
	got, err = s.List(ctx, "media/abc/")
	if err != nil {
		t.Fatalf("List(media/abc/): %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List(media/abc/) returned %d objects, want 2", len(got))
	}

	// A prefix nothing matches is an empty result, not an error.
	got, err = s.List(ctx, "media/nothing/")
	if err != nil {
		t.Fatalf("List(media/nothing/): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List(media/nothing/) returned %d objects, want none", len(got))
	}
}

// The paginator is the whole reason List is written the way it is: S3
// returns at most 1000 keys per call, and a bucket adopted into an empty
// database is exactly where more than that shows up. A single-page
// implementation would silently adopt the first thousand.
func TestS3ListPaginatesPastOnePage(t *testing.T) {
	if testing.Short() {
		t.Skip("media: skipping the paginated listing in -short mode")
	}
	s := newS3TestStore(t, S3Config{})
	ctx := context.Background()

	const n = 1005
	for i := range n {
		put(t, s, fmt.Sprintf("media/item%04d/original.png", i), "image/png", "x")
	}

	got, err := s.List(ctx, "media/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != n {
		t.Errorf("List returned %d objects, want all %d", len(got), n)
	}
}

// A deployment prefix namespaces one site's uploads inside a shared
// bucket. Keys carry it, and a listing scoped to one site's media root
// does not see another's.
func TestS3KeyPrefixNamespacesASharedBucket(t *testing.T) {
	bucket := "cms-shared-bucket"
	acme := newS3TestStore(t, S3Config{Bucket: bucket, KeyPrefix: "acme"})
	ctx := context.Background()

	if got := acme.KeyPrefix(); got != "acme" {
		t.Errorf("KeyPrefix() = %q, want acme", got)
	}
	put(t, acme, keyRoot("acme")+"abc/original.png", "image/png", "acme's")
	put(t, acme, keyRoot("zenith")+"def/original.png", "image/png", "zenith's")

	got, err := acme.List(ctx, keyRoot("acme"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Key != "acme/media/abc/original.png" {
		t.Errorf("List(acme/media/) = %v, want only acme's object", got)
	}

	// And a proxied URL leaves the prefix out, so it never reaches a page.
	if url := acme.PublicURL("acme/media/abc/original.png"); url != ProxyPathPrefix+"abc/original.png" {
		t.Errorf("PublicURL = %q, want the prefix stripped", url)
	}
}

// The bucket policy is the supported way to publish uploads on stores
// that reject per-object ACLs. It is scoped to this deployment's media
// root, so one site sharing a bucket cannot publish another's objects —
// and it must be idempotent, since site setup may run more than once.
func TestS3ApplyPublicReadPolicy(t *testing.T) {
	s := newS3TestStore(t, S3Config{Bucket: "cms-public-policy", KeyPrefix: "acme"})
	ctx := context.Background()
	put(t, s, "acme/media/abc/original.png", "image/png", "public bytes")
	put(t, s, "acme/manifests/abc.json", "application/json", "{}")

	base := "http://" + s.cfg.Endpoint + "/" + s.cfg.Bucket + "/"
	// Without the policy the bucket is private, which is what makes the
	// check after it worth anything.
	if code, _ := anonGet(t, base+"acme/media/abc/original.png"); code == http.StatusOK {
		t.Fatal("the bucket was already public before the policy was applied")
	}

	if err := s.ApplyPublicReadPolicy(ctx); err != nil {
		t.Fatalf("ApplyPublicReadPolicy: %v", err)
	}
	// Idempotent: setup that runs twice must not be a failure.
	if err := s.ApplyPublicReadPolicy(ctx); err != nil {
		t.Fatalf("ApplyPublicReadPolicy (second call): %v", err)
	}

	// The grant is real: an unauthenticated GET reaches the object.
	if code, body := anonGet(t, base+"acme/media/abc/original.png"); code != http.StatusOK {
		t.Errorf("anonymous GET of a media object = %d, want 200 (%s)", code, body)
	} else if body != "public bytes" {
		t.Errorf("anonymous GET returned %q, want the object", body)
	}

	// And it is scoped: the manifests name uploaders and live outside the
	// media root, so the policy must not reach them.
	if code, _ := anonGet(t, base+"acme/manifests/abc.json"); code == http.StatusOK {
		t.Error("the public-read policy published the manifests")
	}
}

// anonGet fetches a URL with no credentials at all, which is the only way
// to tell whether a bucket policy really granted public read.
func anonGet(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		t.Fatalf("reading the response: %v", err)
	}
	return resp.StatusCode, string(body)
}

// A store pointed at a bucket that is not there fails rather than
// pretending: an install misconfigured this way should say so on its
// first upload, not swallow media.
func TestS3AgainstAMissingBucket(t *testing.T) {
	s := newS3TestStore(t, S3Config{})
	ctx := context.Background()
	s.cfg.Bucket = "cms-no-such-bucket"

	if err := s.Put(ctx, "media/abc/original.png", "image/png", bytes.NewReader([]byte("x"))); err == nil {
		t.Error("Put into a missing bucket succeeded")
	}
	if _, err := s.List(ctx, "media/"); err == nil {
		t.Error("List of a missing bucket succeeded")
	}
	// A missing bucket is not a missing object: a caller branching on
	// ErrObjectNotFound would quietly treat the whole store being gone
	// as one absent file.
	if _, _, err := s.Get(ctx, "media/abc/original.png"); errors.Is(err, ErrObjectNotFound) {
		t.Error("a missing bucket read as a missing object")
	}
}
