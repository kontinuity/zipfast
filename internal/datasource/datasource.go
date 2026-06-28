// Package datasource abstracts file storage (local filesystem or S3-compatible).
package datasource

import "io"

// PutOptions carries optional metadata for a stored object.
type PutOptions struct {
	Mimetype string
}

// Datasource is the storage backend contract. Implementations must keep object
// keys identical to the original Zipline ("{name}", thumbnails ".thumbnail.{id}")
// so existing uploads resolve unchanged.
type Datasource interface {
	// Get returns a reader for the whole object, or (nil, nil) if not found.
	Get(file string) (io.ReadCloser, error)
	// Put stores data. size may be -1 if unknown (local), but should be provided for S3.
	Put(file string, r io.Reader, size int64, opts PutOptions) error
	Delete(file string) error
	// Size returns the object size, or -1 if it doesn't exist.
	Size(file string) (int64, error)
	TotalSize() (int64, error)
	Clear() error
	// Range returns a reader for bytes [start, end] inclusive.
	Range(file string, start, end int64) (io.ReadCloser, error)
	Rename(from, to string) error
	List(prefix string) ([]string, error)
}
