package geoip

import (
	"net"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// cacheEntry holds a cached lookup result.
type cacheEntry struct {
	loc     Location
	expires time.Time
}

// CachingDB wraps a DB implementation and caches Lookup results in an
// in-memory LRU cache.
type CachingDB struct {
	db    DB
	ttl   time.Duration
	cache *lru.Cache[string, cacheEntry]
}

// NewCachingDB wraps db with an in-memory LRU cache holding up to size
// entries. ttl controls how long an entry remains valid; a ttl of 0 means
// entries never expire on their own (they'll still be evicted under LRU
// pressure or cleared on Reload).
func NewCachingDB(db DB, size int, ttl time.Duration) (*CachingDB, error) {
	c, err := lru.New[string, cacheEntry](size)
	if err != nil {
		return nil, err
	}

	return &CachingDB{
		db:    db,
		ttl:   ttl,
		cache: c,
	}, nil
}

// Lookup will return the cached result of a previous Lookup if found or
// perform a Lookup against the underlying DB and then cache and return the
// result. Lookups that return an error are not cached.
func (c *CachingDB) Lookup(ip net.IP) (Location, error) {
	key := ip.String()

	if entry, ok := c.cache.Get(key); ok {
		if c.ttl == 0 || time.Now().Before(entry.expires) {
			return entry.loc, nil
		}
	}

	loc, err := c.db.Lookup(ip)
	if err != nil {
		return loc, err
	}

	c.cache.Add(key, cacheEntry{
		loc:     loc,
		expires: time.Now().Add(c.ttl),
	})

	return loc, nil
}

// Reload delegates to the underlying DB and purges the cache on success,
// since the underlying data may have changed.
func (c *CachingDB) Reload() (bool, error) {
	reloaded, err := c.db.Reload()
	if err != nil {
		return reloaded, err
	}

	if reloaded {
		c.cache.Purge()
	}

	return reloaded, err
}
