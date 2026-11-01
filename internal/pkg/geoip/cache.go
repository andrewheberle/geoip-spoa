package geoip

import (
	"net"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// cacheEntry holds a cached lookup result.
type cacheEntry struct {
	loc     Location
	expires time.Time
}

// lookupCache is an in-memory LRU cache of successful lookups.
type lookupCache struct {
	ttl   time.Duration
	cache *lru.Cache[string, cacheEntry]

	// generation is incremented on every purge so a lookup that raced a
	// purge does not store a result read from the old databases.
	generation atomic.Uint64
}

// newLookupCache returns a cache holding up to size entries, or nil if size
// is not positive. A ttl of 0 means entries never expire on their own.
func newLookupCache(size int, ttl time.Duration) *lookupCache {
	if size <= 0 {
		return nil
	}

	c, err := lru.New[string, cacheEntry](size)
	if err != nil {
		// lru.New only fails for a non-positive size
		return nil
	}

	return &lookupCache{
		ttl:   ttl,
		cache: c,
	}
}

// lookup returns the cached Location for ip if present and not expired,
// otherwise it calls fn and caches the result. Results that come with an
// error are not cached.
func (c *lookupCache) lookup(ip net.IP, fn func(net.IP) (Location, error)) (Location, error) {
	key := ip.String()

	if entry, ok := c.cache.Get(key); ok {
		if c.ttl == 0 || time.Now().Before(entry.expires) {
			return entry.loc, nil
		}
	}

	gen := c.generation.Load()

	loc, err := fn(ip)
	if err != nil {
		return loc, err
	}

	if c.generation.Load() == gen {
		c.cache.Add(key, cacheEntry{
			loc:     loc,
			expires: time.Now().Add(c.ttl),
		})
	}

	return loc, nil
}

// purge removes every entry from the cache.
func (c *lookupCache) purge() {
	c.generation.Add(1)
	c.cache.Purge()
}
