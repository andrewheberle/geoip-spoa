package geoip

import (
	"net"
	"time"

	"github.com/IncSW/geoip2"
	lru "github.com/hashicorp/golang-lru/v2"
)

// cacheEntry holds a cached lookup result.
type cacheEntry struct {
	asn     *geoip2.ASN
	city    *geoip2.CityResult
	err     error
	expires time.Time
}

// CachingDB wraps a DB implementation and caches Lookup results in an
// in-memory LRU cache.
type CachingDB struct {
	underlying DB
	ttl        time.Duration
	cache      *lru.Cache[string, cacheEntry]
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
		underlying: db,
		ttl:        ttl,
		cache:      c,
	}, nil
}

func (c *CachingDB) Lookup(ip net.IP) (*geoip2.ASN, *geoip2.CityResult, error) {
	key := ip.String()

	if entry, ok := c.cache.Get(key); ok {
		if c.ttl == 0 || time.Now().Before(entry.expires) {
			return entry.asn, entry.city, entry.err
		}
	}

	asn, city, err := c.underlying.Lookup(ip)

	c.cache.Add(key, cacheEntry{
		asn:     asn,
		city:    city,
		err:     err,
		expires: time.Now().Add(c.ttl),
	})

	return asn, city, err
}

// Reload delegates to the underlying DB and purges the cache on success,
// since the underlying data may have changed.
func (c *CachingDB) Reload() (bool, error) {
	reloaded, err := c.underlying.Reload()
	if err != nil {
		return reloaded, err
	}

	if reloaded {
		c.cache.Purge()
	}

	return reloaded, err
}
