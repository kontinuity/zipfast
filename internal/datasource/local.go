package datasource

import (
	"io"
	"os"
	"path/filepath"
)

// Local stores files on the local filesystem under Directory.
type Local struct {
	Directory string
}

// NewLocal creates a Local datasource, ensuring the directory exists.
func NewLocal(dir string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Local{Directory: dir}, nil
}

func (l *Local) path(file string) string {
	return filepath.Join(l.Directory, file)
}

func (l *Local) Get(file string) (io.ReadCloser, error) {
	f, err := os.Open(l.path(file))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

func (l *Local) Put(file string, r io.Reader, _ int64, _ PutOptions) error {
	p := l.path(file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

func (l *Local) Delete(file string) error {
	err := os.Remove(l.path(file))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (l *Local) Size(file string) (int64, error) {
	fi, err := os.Stat(l.path(file))
	if err != nil {
		if os.IsNotExist(err) {
			return -1, nil
		}
		return -1, err
	}
	return fi.Size(), nil
}

func (l *Local) TotalSize() (int64, error) {
	var total int64
	err := filepath.Walk(l.Directory, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func (l *Local) Clear() error {
	entries, err := os.ReadDir(l.Directory)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(l.path(e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// rangeReadCloser pairs a *os.File with a limited reader so the file is closed.
type rangeReadCloser struct {
	io.Reader
	f *os.File
}

func (rc *rangeReadCloser) Close() error { return rc.f.Close() }

func (l *Local) Range(file string, start, end int64) (io.ReadCloser, error) {
	f, err := os.Open(l.path(file))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	length := end - start + 1
	return &rangeReadCloser{Reader: io.LimitReader(f, length), f: f}, nil
}

func (l *Local) Rename(from, to string) error {
	return os.Rename(l.path(from), l.path(to))
}

func (l *Local) List(prefix string) ([]string, error) {
	var out []string
	err := filepath.Walk(l.Directory, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(l.Directory, p)
		if prefix == "" || filepathHasPrefix(rel, prefix) {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

func filepathHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
