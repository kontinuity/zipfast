package datasource

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"

	"zipfast/internal/config"
	"zipfast/internal/logger"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 stores files in an S3-compatible bucket via minio-go. Object keys mirror
// the original Zipline layout, optionally nested under a configured Subdirectory
// prefix.
type S3 struct {
	client *minio.Client
	bucket string
	// prefix is the (optionally empty) Subdirectory under which every object key
	// is stored. It never carries a leading/trailing slash.
	prefix string
	// log carries a component-scoped slog logger. Read paths emit debug records
	// (visible with LOG_LEVEL=debug) describing the exact key queried, which is
	// the fastest way to diagnose a "file is in the bucket but the frontend gets
	// a 404" mismatch — almost always a Subdirectory prefix discrepancy.
	log *slog.Logger
}

// ensure S3 satisfies the Datasource contract at compile time.
var _ Datasource = (*S3)(nil)

// NewS3 constructs an S3 datasource from config and verifies connectivity. If
// cfg.Endpoint is empty it defaults to AWS ("s3.amazonaws.com" over TLS).
func NewS3(cfg config.S3DS) (*S3, error) {
	endpoint := cfg.Endpoint
	useSSL := true
	if endpoint == "" {
		// Default to AWS S3, which is always TLS.
		endpoint = "s3.amazonaws.com"
		useSSL = true
	}

	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: useSSL,
		Region: cfg.Region,
	}
	// Force path-style addressing (bucket in the path, not the host) when asked;
	// required by many non-AWS S3 implementations (MinIO, Ceph, etc.).
	if cfg.ForcePathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}

	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("s3: create client: %w", err)
	}

	s := &S3{
		client: client,
		bucket: cfg.Bucket,
		prefix: strings.Trim(cfg.Subdirectory, "/"),
		log:    logger.Log("datasource.s3"),
	}
	s.log.Debug("s3 datasource initialised",
		"endpoint", endpoint, "bucket", s.bucket, "prefix", s.prefix,
		"region", cfg.Region, "force_path_style", cfg.ForcePathStyle)

	// Verify connectivity (and that the bucket exists) up front so misconfiguration
	// fails loudly at startup rather than on the first upload.
	exists, err := client.BucketExists(context.Background(), s.bucket)
	if err != nil {
		return nil, fmt.Errorf("s3: bucket check failed for %q: %w", s.bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("s3: bucket %q does not exist", s.bucket)
	}

	return s, nil
}

// key applies the Subdirectory prefix to an object name. It always uses forward
// slashes (S3 keys are not OS paths), so path.Join is used rather than filepath.
func (s *S3) key(name string) string {
	if s.prefix == "" {
		return name
	}
	return path.Join(s.prefix, name)
}

// stripPrefix removes the Subdirectory prefix from a key so returned names match
// how Local reports them (relative to the storage root).
func (s *S3) stripPrefix(key string) string {
	if s.prefix == "" {
		return key
	}
	return strings.TrimPrefix(key, s.prefix+"/")
}

// isNoSuchKey reports whether err is an S3 "object not found" error.
func isNoSuchKey(err error) bool {
	code := minio.ToErrorResponse(err).Code
	return code == "NoSuchKey" || code == "NoSuchObject"
}

// logLookup emits a debug record describing how a read resolved. It always
// records the raw name, the configured bucket/prefix, and the fully resolved
// object key, so a request that 404s can be compared against what actually
// lives in the bucket. On error it also surfaces the S3 error code and HTTP
// status (a NoSuchKey 404 vs. an AccessDenied 403 look identical to the caller
// but mean very different things). This is intentionally debug-level so it stays
// silent in production until LOG_LEVEL=debug is set.
func (s *S3) logLookup(op, file, key string, err error) {
	if err == nil {
		s.log.Debug("s3 lookup hit",
			"op", op, "bucket", s.bucket, "prefix", s.prefix, "file", file, "key", key)
		return
	}
	resp := minio.ToErrorResponse(err)
	s.log.Debug("s3 lookup miss",
		"op", op, "bucket", s.bucket, "prefix", s.prefix, "file", file, "key", key,
		"not_found", isNoSuchKey(err),
		"s3_code", resp.Code, "http_status", resp.StatusCode, "err", err.Error())
}

// Get returns a reader for the whole object, or (nil, nil) if it does not exist.
func (s *S3) Get(file string) (io.ReadCloser, error) {
	ctx := context.Background()
	key := s.key(file)

	// StatObject first so a missing object is reported as (nil, nil) instead of
	// surfacing lazily on the first Read of the returned stream.
	if _, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err != nil {
		s.logLookup("Get", file, key, err)
		if isNoSuchKey(err) {
			return nil, nil
		}
		return nil, err
	}

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		s.logLookup("Get", file, key, err)
		if isNoSuchKey(err) {
			return nil, nil
		}
		return nil, err
	}
	s.logLookup("Get", file, key, nil)
	return obj, nil
}

// Put stores data under file. A negative size means "unknown length"; minio
// streams the body in that case.
func (s *S3) Put(file string, r io.Reader, size int64, opts PutOptions) error {
	if size < 0 {
		size = -1
	}
	_, err := s.client.PutObject(context.Background(), s.bucket, s.key(file), r, size,
		minio.PutObjectOptions{ContentType: opts.Mimetype})
	return err
}

// Delete removes an object. A missing object is not treated as an error.
func (s *S3) Delete(file string) error {
	err := s.client.RemoveObject(context.Background(), s.bucket, s.key(file),
		minio.RemoveObjectOptions{})
	if err != nil && isNoSuchKey(err) {
		return nil
	}
	return err
}

// Size returns the object size, or -1 if it does not exist.
func (s *S3) Size(file string) (int64, error) {
	key := s.key(file)
	info, err := s.client.StatObject(context.Background(), s.bucket, key,
		minio.StatObjectOptions{})
	if err != nil {
		s.logLookup("Size", file, key, err)
		if isNoSuchKey(err) {
			return -1, nil
		}
		return -1, err
	}
	return info.Size, nil
}

// TotalSize sums the sizes of every object under the configured prefix.
func (s *S3) TotalSize() (int64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var total int64
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    s.prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return total, obj.Err
		}
		total += obj.Size
	}
	return total, nil
}

// Clear removes every object under the configured prefix.
func (s *S3) Clear() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	objectsCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objectsCh)
		for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
			Prefix:    s.prefix,
			Recursive: true,
		}) {
			if obj.Err != nil {
				// Stop feeding on a listing error; cancelling the context unblocks
				// RemoveObjects and surfaces the failure to the caller.
				cancel()
				return
			}
			objectsCh <- obj
		}
	}()

	for rerr := range s.client.RemoveObjects(ctx, s.bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		if rerr.Err != nil {
			return rerr.Err
		}
	}
	return nil
}

// s3RangeReadCloser pairs the byte-range reader with the underlying object so the
// object's resources are released on Close.
type s3RangeReadCloser struct {
	io.Reader
	obj *minio.Object
}

func (rc *s3RangeReadCloser) Close() error { return rc.obj.Close() }

// Range returns a reader for bytes [start, end] inclusive, or (nil, nil) if the
// object does not exist.
func (s *S3) Range(file string, start, end int64) (io.ReadCloser, error) {
	ctx := context.Background()
	key := s.key(file)

	getOpts := minio.GetObjectOptions{}
	if err := getOpts.SetRange(start, end); err != nil {
		return nil, err
	}

	obj, err := s.client.GetObject(ctx, s.bucket, key, getOpts)
	if err != nil {
		s.logLookup("Range", file, key, err)
		if isNoSuchKey(err) {
			return nil, nil
		}
		return nil, err
	}

	// GetObject is lazy, so confirm existence via Stat to honour the (nil, nil)
	// not-found contract before returning the stream.
	if _, err := obj.Stat(); err != nil {
		s.logLookup("Range", file, key, err)
		obj.Close()
		if isNoSuchKey(err) {
			return nil, nil
		}
		return nil, err
	}

	s.log.Debug("s3 lookup hit",
		"op", "Range", "bucket", s.bucket, "prefix", s.prefix, "file", file, "key", key,
		"start", start, "end", end)
	length := end - start + 1
	return &s3RangeReadCloser{Reader: io.LimitReader(obj, length), obj: obj}, nil
}

// Rename copies the object to the new key and deletes the original (S3 has no
// native move operation).
func (s *S3) Rename(from, to string) error {
	ctx := context.Background()

	dst := minio.CopyDestOptions{Bucket: s.bucket, Object: s.key(to)}
	src := minio.CopySrcOptions{Bucket: s.bucket, Object: s.key(from)}
	if _, err := s.client.CopyObject(ctx, dst, src); err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, s.bucket, s.key(from), minio.RemoveObjectOptions{})
}

// List returns the keys under the combined (Subdirectory + prefix) prefix, with
// the Subdirectory stripped so names match Local's output.
func (s *S3) List(prefix string) ([]string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolved := s.key(prefix)
	var out []string
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    resolved,
		Recursive: true,
	}) {
		if obj.Err != nil {
			s.log.Debug("s3 list error",
				"bucket", s.bucket, "requested_prefix", prefix, "resolved_prefix", resolved,
				"err", obj.Err.Error())
			return out, obj.Err
		}
		out = append(out, s.stripPrefix(obj.Key))
	}
	s.log.Debug("s3 list",
		"bucket", s.bucket, "requested_prefix", prefix, "resolved_prefix", resolved,
		"count", len(out))
	return out, nil
}
