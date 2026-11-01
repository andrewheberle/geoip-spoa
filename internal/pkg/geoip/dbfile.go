package geoip

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/zeebo/xxh3"
)

// reader looks up IP addresses in one loaded database.
type reader[T any] interface {
	Lookup(ip net.IP) (T, error)
}

// dbFile is one GeoLite2 database file that can be looked up and hot-swapped
// when the file on disk changes. An empty path means the database is not
// configured: lookups fail and reloads do nothing.
type dbFile[T any] struct {
	name string
	path string
	open func(path string) (reader[T], error)

	mu     sync.RWMutex
	reader reader[T]
	hash   string
}

// openDBFile loads the database at path using open. name is used in errors.
func openDBFile[T any](name, path string, open func(path string) (reader[T], error)) (*dbFile[T], error) {
	f := &dbFile[T]{
		name: name,
		path: path,
		open: open,
	}

	if path == "" {
		return f, nil
	}

	r, hash, err := f.load()
	if err != nil {
		return nil, err
	}

	f.reader = r
	f.hash = hash

	return f, nil
}

func (f *dbFile[T]) load() (reader[T], string, error) {
	hash, err := hashFile(f.path)
	if err != nil {
		return nil, "", fmt.Errorf("could not hash %s database: %w", f.name, err)
	}

	r, err := f.open(f.path)
	if err != nil {
		return nil, "", fmt.Errorf("could not load %s database: %w", f.name, err)
	}

	return r, hash, nil
}

func (f *dbFile[T]) lookup(ip net.IP) (T, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.reader == nil {
		var zero T
		return zero, fmt.Errorf("no %s database loaded", f.name)
	}

	return f.reader.Lookup(ip)
}

// reload loads the file again if its hash has changed and reports whether it
// did. The current database is only replaced if the new one loads.
func (f *dbFile[T]) reload() (bool, error) {
	if f.path == "" {
		return false, nil
	}

	hash, err := hashFile(f.path)
	if err != nil {
		return false, fmt.Errorf("could not hash %s database: %w", f.name, err)
	}

	f.mu.RLock()
	unchanged := f.hash == hash
	f.mu.RUnlock()

	if unchanged {
		return false, nil
	}

	r, err := f.open(f.path)
	if err != nil {
		return false, fmt.Errorf("could not load %s database: %w", f.name, err)
	}

	f.mu.Lock()
	f.reader = r
	f.hash = hash
	f.mu.Unlock()

	return true, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	hasher := xxh3.New()

	// 1MB buffer balances syscall overhead vs memory use.
	// Avoid io.Copy's default small buffer for large files.
	buf := make([]byte, 1<<20)
	if _, err := io.CopyBuffer(hasher, f, buf); err != nil {
		return "", fmt.Errorf("hashing file: %w", err)
	}

	sum := hasher.Sum128()
	return fmt.Sprintf("%016x%016x", sum.Hi, sum.Lo), nil
}
