// Package media stores uploads — images, videos, and documents: the binary
// objects on any S3-compatible bucket (AWS, Linode, DigitalOcean, MinIO,
// R2, ...) and their metadata in Postgres. Each image upload produces the
// untouched original plus resized "web" and "thumb" variants encoded as
// lossy WebP; videos are stored as uploaded (no transcoding) with an
// optional poster frame; documents are stored as-is. Everything is served
// directly from the bucket, or proxied by the CMS.
package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// ObjectStore is where media binaries live. The S3 implementation is the
// default; host applications may substitute their own (e.g. local disk for
// development) — it is one of the module's extension points.
type ObjectStore interface {
	// Put stores an object under key.
	Put(ctx context.Context, key, contentType string, body io.Reader) error
	// Get retrieves the object at key and its content type. The caller
	// closes the body.
	Get(ctx context.Context, key string) (body io.ReadCloser, contentType string, err error)
	// Delete removes the object at key. Deleting a missing key is not an
	// error.
	Delete(ctx context.Context, key string) error
	// PublicURL returns the browser-facing URL for key. It may be
	// absolute (bucket or CDN) or app-relative (proxied through the CMS).
	PublicURL(key string) string
}

// ErrObjectNotFound is returned by Get for missing keys.
var ErrObjectNotFound = errors.New("media: object not found")

// ErrInvalidRange is returned by GetRange for unsatisfiable ranges; the
// media proxy answers it with 416.
var ErrInvalidRange = errors.New("media: invalid range")

// RangeGetter is an optional ObjectStore interface for serving HTTP Range
// requests. Without it, proxied video cannot seek — and Safari, which
// probes with a Range request before playing, cannot play it at all.
// S3Store implements it; custom stores that skip it still work for images
// and documents.
type RangeGetter interface {
	// GetRange retrieves part of the object at key, where rangeSpec is a
	// verbatim HTTP Range header value ("bytes=0-1023"). contentRange is
	// the Content-Range for a 206 response; when the backend ignored the
	// range (e.g. a multi-range request) it is empty and body is the
	// whole object. length is the byte count of body. Unsatisfiable
	// ranges return ErrInvalidRange.
	GetRange(ctx context.Context, key, rangeSpec string) (body io.ReadCloser, contentType, contentRange string, length int64, err error)
}

// KeyPrefixer is an optional interface an ObjectStore may implement to
// namespace one deployment's objects inside a bucket shared by several
// sites: when present, the Manager stores objects under
// "<KeyPrefix()>/media/..." instead of "media/...". S3Store implements it
// from S3Config.KeyPrefix.
type KeyPrefixer interface {
	KeyPrefix() string
}

// keyRoot is the bucket prefix all of a deployment's media objects live
// under: "media/", or "<prefix>/media/" when a deployment prefix is set.
func keyRoot(prefix string) string {
	if prefix == "" {
		return "media/"
	}
	return prefix + "/media/"
}

// S3Config configures the S3-compatible object store.
type S3Config struct {
	// Endpoint is the S3 API host, without scheme, e.g.
	// "us-ord-10.linodeobjects.com" or "s3.us-east-1.amazonaws.com".
	Endpoint string
	// Region for request signing. Defaults to the first label of
	// Endpoint (correct for Linode/DO-style endpoints); set explicitly
	// for AWS.
	Region string
	Bucket string
	// AccessKey and Secret are the credentials.
	AccessKey string
	Secret    string
	// KeyPrefix namespaces this deployment's uploads inside a bucket
	// shared by several sites: objects are stored under
	// "<KeyPrefix>/media/..." instead of "media/...". Use a short slug
	// unique to the deployment — letters, digits, '.', '-', '_' — e.g.
	// "acme-hotel". Once media has been uploaded it must never change:
	// stored object keys embed it. Proxied media URLs (the default) do
	// not expose it; direct bucket and CDN URLs include it. A shared
	// bucket pairs well with per-deployment credentials restricted to
	// this prefix. Empty — the default — keeps keys under "media/".
	KeyPrefix string
	// PublicRead marks the bucket as publicly readable, so pages embed
	// direct bucket URLs. Leave false — the default — to serve media
	// through the CMS itself (the /cms/media/ route on the public
	// handler), which works with private buckets and needs no bucket
	// policy. Making a bucket public varies by provider: a bucket
	// policy allowing s3:GetObject on AWS, the bucket Access setting on
	// Linode/DO, or ApplyPublicReadPolicy where permitted.
	PublicRead bool

	// PublicBaseURL overrides the generated object URL prefix, for
	// serving through a CDN or custom domain. No trailing slash. Takes
	// precedence over PublicRead.
	PublicBaseURL string
	// UsePathStyle addresses objects as endpoint/bucket/key instead of
	// bucket.endpoint/key. Needed for MinIO and some self-hosted stores.
	UsePathStyle bool
	// ObjectACL is an optional canned ACL (e.g. "public-read") sent with
	// each upload. Leave empty — the default — for buckets whose public
	// access comes from a bucket policy; many stores (AWS with ACLs
	// disabled, newer Linode clusters) reject per-object ACLs outright.
	// See ApplyPublicReadPolicy for setting the bucket policy.
	ObjectACL string
}

// S3Store implements ObjectStore against any S3-compatible service.
type S3Store struct {
	client *s3.Client
	cfg    S3Config
}

// NewS3Store validates cfg and returns a ready S3Store. It does not call
// the network.
func NewS3Store(cfg S3Config) (*S3Store, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.Secret == "" {
		return nil, fmt.Errorf("media: S3 Endpoint, Bucket, AccessKey, and Secret are all required")
	}
	if !validKeyPrefix(cfg.KeyPrefix) {
		return nil, fmt.Errorf("media: S3 KeyPrefix %q may only contain letters, digits, '.', '-', and '_'", cfg.KeyPrefix)
	}
	if cfg.Region == "" {
		cfg.Region = regionFromEndpoint(cfg.Endpoint)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.Secret, "")),
		// The SDK's default streaming checksums (aws-chunked + CRC32
		// trailers) are rejected by many S3-compatible stores (Ceph:
		// Linode, DigitalOcean, ...); compute them only when an
		// operation requires one.
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
		awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		return nil, fmt.Errorf("media: building S3 config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("https://" + cfg.Endpoint)
		o.UsePathStyle = cfg.UsePathStyle
	})
	return &S3Store{client: client, cfg: cfg}, nil
}

// validKeyPrefix reports whether p is safe to embed in object keys and
// URLs: letters, digits, '.', '-', '_', with no ".." runs. Empty is valid
// (prefixing disabled).
func validKeyPrefix(p string) bool {
	if strings.Contains(p, "..") {
		return false
	}
	for _, r := range p {
		switch {
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9',
			r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// regionFromEndpoint guesses a signing region from endpoints like
// "us-ord-10.linodeobjects.com". S3-compatible providers generally accept
// their cluster label as the region.
func regionFromEndpoint(endpoint string) string {
	for i := range endpoint {
		if endpoint[i] == '.' {
			return endpoint[:i]
		}
	}
	return "us-east-1"
}

func (s *S3Store) Put(ctx context.Context, key, contentType string, body io.Reader) error {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
		// Keys are unique per upload, so objects are immutable.
		CacheControl: aws.String("public, max-age=31536000, immutable"),
	}
	if s.cfg.ObjectACL != "" {
		input.ACL = types.ObjectCannedACL(s.cfg.ObjectACL)
	}
	if _, err := s.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("media: putting %s: %w", key, err)
	}
	return nil
}

// ApplyPublicReadPolicy sets a bucket policy that lets anyone GET objects
// (but not list or write). Call it once during site setup when the bucket
// was not created public; it is idempotent. This is the supported way to
// serve uploads publicly on stores that reject per-object ACLs.
func (s *S3Store) ApplyPublicReadPolicy(ctx context.Context) error {
	policy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [{
			"Sid": "CMSPublicRead",
			"Effect": "Allow",
			"Principal": "*",
			"Action": ["s3:GetObject"],
			"Resource": ["arn:aws:s3:::%s/*"]
		}]
	}`, s.cfg.Bucket)
	_, err := s.client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(s.cfg.Bucket),
		Policy: aws.String(policy),
	})
	if err != nil {
		return fmt.Errorf("media: applying public-read bucket policy: %w", err)
	}
	return nil
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noKey *types.NoSuchKey
		if errors.As(err, &noKey) {
			return nil, "", ErrObjectNotFound
		}
		return nil, "", fmt.Errorf("media: getting %s: %w", key, err)
	}
	return out.Body, aws.ToString(out.ContentType), nil
}

// GetRange implements RangeGetter by forwarding the Range header to S3.
func (s *S3Store) GetRange(ctx context.Context, key, rangeSpec string) (io.ReadCloser, string, string, int64, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
		Range:  aws.String(rangeSpec),
	})
	if err != nil {
		var noKey *types.NoSuchKey
		if errors.As(err, &noKey) {
			return nil, "", "", 0, ErrObjectNotFound
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidRange" {
			return nil, "", "", 0, ErrInvalidRange
		}
		return nil, "", "", 0, fmt.Errorf("media: getting %s: %w", key, err)
	}
	return out.Body, aws.ToString(out.ContentType), aws.ToString(out.ContentRange), aws.ToInt64(out.ContentLength), nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("media: deleting %s: %w", key, err)
	}
	return nil
}

// ProxyPathPrefix is the public route the CMS serves proxied media under.
const ProxyPathPrefix = "/cms/media/"

// KeyPrefix returns the configured deployment prefix, implementing
// KeyPrefixer.
func (s *S3Store) KeyPrefix() string {
	return s.cfg.KeyPrefix
}

func (s *S3Store) PublicURL(key string) string {
	switch {
	case s.cfg.PublicBaseURL != "":
		return s.cfg.PublicBaseURL + "/" + key
	case !s.cfg.PublicRead:
		// Served by the CMS's own media route; relative so it works on
		// any host. The proxy re-adds the key root, so a deployment
		// prefix never shows in page URLs.
		return ProxyPathPrefix + strings.TrimPrefix(key, keyRoot(s.cfg.KeyPrefix))
	case s.cfg.UsePathStyle:
		return "https://" + s.cfg.Endpoint + "/" + s.cfg.Bucket + "/" + key
	default:
		return "https://" + s.cfg.Bucket + "." + s.cfg.Endpoint + "/" + key
	}
}
