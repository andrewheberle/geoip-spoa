package geoip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/IncSW/geoip2"
	"github.com/zeebo/xxh3"
)

// Location holds the geographic and network facts about one IP address that
// are returned to HAProxy. Any value that could not be found is left as its
// zero value.
type Location struct {
	ASN       uint32
	Org       string
	Continent string
	Country   string
	City      string
}

// DB looks up the Location of IP addresses.
type DB interface {
	// Lookup returns the Location of ip. An IP address that is missing from
	// one or both databases is not an error: the fields that could not be
	// found are left empty. An error is only returned for a real failure,
	// in which case any fields that were found are still populated.
	Lookup(ip net.IP) (Location, error)
}

var _ DB = &GeoDB{}

type GeoDB struct {
	asnMu  sync.RWMutex
	cityMu sync.RWMutex

	asn      *geoip2.ASNReader
	asnHash  string
	asnPath  string
	city     *geoip2.CityReader
	cityHash string
	cityPath string

	locale   string
	logger   *slog.Logger
	cache    *lookupCache
	interval time.Duration
}

// Option configures a GeoDB.
type Option func(*GeoDB)

// WithLocale sets the locale used for City names. The default is "en".
func WithLocale(locale string) Option {
	return func(d *GeoDB) {
		d.locale = locale
	}
}

// WithLogger sets the logger. By default nothing is logged.
func WithLogger(logger *slog.Logger) Option {
	return func(d *GeoDB) {
		d.logger = logger
	}
}

// WithCache caches up to size successful lookups in memory. A ttl of 0 means
// entries never expire on their own; they are still evicted when the cache
// is full and cleared whenever Reload loads a changed database. A size of 0
// or less disables the cache, which is the default.
func WithCache(size int, ttl time.Duration) Option {
	return func(d *GeoDB) {
		d.cache = newLookupCache(size, ttl)
	}
}

// WithReloadInterval sets how often Run checks for changed database files.
// An interval of 0 or less disables reloading, which is the default.
func WithReloadInterval(interval time.Duration) Option {
	return func(d *GeoDB) {
		d.interval = interval
	}
}

// Open loads the GeoLite2 ASN and City databases from the given paths. An
// empty path skips loading that database.
func Open(asnpath, citypath string, opts ...Option) (*GeoDB, error) {
	db := &GeoDB{
		asnPath:  asnpath,
		cityPath: citypath,
		locale:   "en",
		logger:   slog.New(slog.DiscardHandler),
	}

	for _, o := range opts {
		o(db)
	}

	if asnpath != "" {
		r, err := geoip2.NewASNReaderFromFile(asnpath)
		if err != nil {
			return nil, fmt.Errorf("could not load ASN database: %w", err)
		}

		hash, err := hashFile(asnpath)
		if err != nil {
			return nil, fmt.Errorf("could not hash asn database for change detection")
		}

		db.asn = r
		db.asnHash = hash
	}

	if citypath != "" {
		r, err := geoip2.NewCityReaderFromFile(citypath)
		if err != nil {
			return nil, fmt.Errorf("could not load city database: %w", err)
		}

		hash, err := hashFile(citypath)
		if err != nil {
			return nil, fmt.Errorf("could not hash city database for change detection")
		}

		db.city = r
		db.cityHash = hash
	}

	return db, nil
}

// Lookup performs a GeoIP lookup against the underlying GeoLite2 ASN and City
// databases, using the cache if one is configured. See DB.Lookup for the
// error contract.
func (d *GeoDB) Lookup(ip net.IP) (Location, error) {
	if d.cache != nil {
		return d.cache.lookup(ip, d.lookup)
	}

	return d.lookup(ip)
}

func (d *GeoDB) lookup(ip net.IP) (Location, error) {
	var loc Location
	errs := make([]error, 0)

	asn, err := d.lookupASN(ip)
	switch {
	case err == nil:
		loc.ASN = asn.AutonomousSystemNumber
		loc.Org = asn.AutonomousSystemOrganization
	case !errors.Is(err, geoip2.ErrNotFound):
		errs = append(errs, fmt.Errorf("could not look up ASN: %w", err))
	}

	city, err := d.lookupCity(ip)
	switch {
	case err == nil:
		loc.Continent = city.Continent.Code
		loc.Country = city.Country.ISOCode
		if name, ok := city.City.Names[d.locale]; ok {
			loc.City = name
		} else if city.City.GeoNameID != 0 {
			d.logger.Warn("city name for chosen locale was not available", "ip", ip, "locale", d.locale, "geonameid", city.City.GeoNameID)
		}
	case !errors.Is(err, geoip2.ErrNotFound):
		errs = append(errs, fmt.Errorf("could not look up City: %w", err))
	}

	return loc, errors.Join(errs...)
}

func (d *GeoDB) lookupASN(ip net.IP) (*geoip2.ASN, error) {
	d.asnMu.RLock()
	defer d.asnMu.RUnlock()

	if d.asn == nil {
		return nil, fmt.Errorf("no ASN database loaded")
	}

	return d.asn.Lookup(ip)
}

func (d *GeoDB) lookupCity(ip net.IP) (*geoip2.CityResult, error) {
	d.cityMu.RLock()
	defer d.cityMu.RUnlock()

	if d.city == nil {
		return nil, fmt.Errorf("no City database loaded")
	}

	return d.city.Lookup(ip)
}

// Reload checks the current on-disk hash of the loaded GeoLite2 databases
// and will reload those files if they have changed.
// The function will return true if one (or both) databases were reloaded
// and any errors during the reload process. The current version of each
// database is only replaced if it could be successfully loaded. The cache is
// cleared whenever either database changed, even if the other failed.
func (d *GeoDB) Reload() (bool, error) {
	errs := make([]error, 0)
	changed := false

	if change, err := d.reloadASN(); err != nil {
		errs = append(errs, err)
	} else {
		if change {
			changed = true
		}
	}

	if change, err := d.reloadCity(); err != nil {
		errs = append(errs, err)
	} else {
		if change {
			changed = true
		}
	}

	if changed && d.cache != nil {
		d.cache.purge()
	}

	return changed, errors.Join(errs...)
}

// Run checks for changed database files every reload interval until ctx is
// cancelled, then returns nil. If reloading is disabled it just waits for
// ctx to be cancelled.
func (d *GeoDB) Run(ctx context.Context) error {
	if d.interval <= 0 {
		<-ctx.Done()
		return nil
	}

	d.logger.Info("starting background reload of GeoLite2 databases", "interval", d.interval, "asn", d.asnPath, "city", d.cityPath)
	t := time.NewTicker(d.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			changed, err := d.Reload()
			switch {
			case err != nil:
				d.logger.Warn("there was an error while reloading databases", "error", err, "changed", changed)
			case changed:
				d.logger.Info("new database version loaded")
			default:
				d.logger.Info("no changes detected to databases")
			}
		}
	}
}

func (d *GeoDB) reloadASN() (bool, error) {
	if d.asnPath == "" {
		return false, nil
	}

	hash, err := hashFile(d.asnPath)
	if err != nil {
		return false, fmt.Errorf("could not hash asn database: %w", err)
	}

	d.asnMu.RLock()
	nochange := d.asnHash == hash
	d.asnMu.RUnlock()

	if nochange {
		return false, nil
	}

	data, err := geoip2.NewASNReaderFromFile(d.asnPath)
	if err != nil {
		return false, fmt.Errorf("could not load new asn database: %w", err)
	}

	d.asnMu.Lock()
	d.asn = data
	d.asnHash = hash
	d.asnMu.Unlock()

	return true, nil
}

func (d *GeoDB) reloadCity() (bool, error) {
	if d.cityPath == "" {
		return false, nil
	}

	hash, err := hashFile(d.cityPath)
	if err != nil {
		return false, fmt.Errorf("could not hash city database: %w", err)
	}

	d.cityMu.RLock()
	nochange := d.cityHash == hash
	d.cityMu.RUnlock()

	if nochange {
		return false, nil
	}

	data, err := geoip2.NewCityReaderFromFile(d.cityPath)
	if err != nil {
		return false, fmt.Errorf("could not load new city database: %w", err)
	}

	d.cityMu.Lock()
	d.city = data
	d.cityHash = hash
	d.cityMu.Unlock()

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
