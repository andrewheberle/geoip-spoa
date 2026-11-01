package geoip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/IncSW/geoip2"
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
	asn  *dbFile[*geoip2.ASN]
	city *dbFile[*geoip2.CityResult]

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
		locale: "en",
		logger: slog.New(slog.DiscardHandler),
	}

	for _, o := range opts {
		o(db)
	}

	asn, err := openDBFile("ASN", asnpath, func(path string) (reader[*geoip2.ASN], error) {
		return geoip2.NewASNReaderFromFile(path)
	})
	if err != nil {
		return nil, err
	}

	city, err := openDBFile("City", citypath, func(path string) (reader[*geoip2.CityResult], error) {
		return geoip2.NewCityReaderFromFile(path)
	})
	if err != nil {
		return nil, err
	}

	db.asn = asn
	db.city = city

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

	asn, err := d.asn.lookup(ip)
	switch {
	case err == nil:
		loc.ASN = asn.AutonomousSystemNumber
		loc.Org = asn.AutonomousSystemOrganization
	case !errors.Is(err, geoip2.ErrNotFound):
		errs = append(errs, fmt.Errorf("could not look up ASN: %w", err))
	}

	city, err := d.city.lookup(ip)
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

// Reload checks the current on-disk hash of the loaded GeoLite2 databases
// and will reload those files if they have changed.
// The function will return true if one (or both) databases were reloaded
// and any errors during the reload process. The current version of each
// database is only replaced if it could be successfully loaded. The cache is
// cleared whenever either database changed, even if the other failed.
func (d *GeoDB) Reload() (bool, error) {
	errs := make([]error, 0)
	changed := false

	for _, f := range []interface{ reload() (bool, error) }{d.asn, d.city} {
		change, err := f.reload()
		if err != nil {
			errs = append(errs, err)
		}
		changed = changed || change
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

	d.logger.Info("starting background reload of GeoLite2 databases", "interval", d.interval, "asn", d.asn.path, "city", d.city.path)
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
