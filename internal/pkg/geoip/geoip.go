package geoip

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/IncSW/geoip2"
	"github.com/zeebo/xxh3"
)

type DB interface {
	Lookup(ip net.IP) (*geoip2.ASN, *geoip2.CityResult, error)
	Reload() (bool, error)
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
}

func Open(asnpath, citypath string) (*GeoDB, error) {
	db := &GeoDB{
		asnPath:  asnpath,
		cityPath: citypath,
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
// databases. If an error occurs during either lookup one (or both) errors are
// returned along with minimal "zero value" geoip2.ASN and geoip2.CityResult
// results rather than returning nil values.
func (d *GeoDB) Lookup(ip net.IP) (*geoip2.ASN, *geoip2.CityResult, error) {
	errs := make([]error, 0)

	asn, err := d.lookupASN(ip)
	if err != nil {
		errs = append(errs, fmt.Errorf("could not look up ASN: %w", err))
		asn = &geoip2.ASN{}
	}

	city, err := d.lookupCity(ip)
	if err != nil {
		errs = append(errs, fmt.Errorf("could not look up City: %w", err))
		city = &geoip2.CityResult{
			City: geoip2.City{
				Names: make(map[string]string),
			},
		}
	}

	return asn, city, errors.Join(errs...)
}

func (d *GeoDB) lookupASN(ip net.IP) (*geoip2.ASN, error) {
	if d.asn == nil {
		return nil, fmt.Errorf("no ASN database loaded")
	}

	d.asnMu.RLock()
	defer d.asnMu.RUnlock()

	return d.asn.Lookup(ip)
}

func (d *GeoDB) lookupCity(ip net.IP) (*geoip2.CityResult, error) {
	if d.city == nil {
		return nil, fmt.Errorf("no City database loaded")
	}

	d.cityMu.RLock()
	defer d.cityMu.RUnlock()

	return d.city.Lookup(ip)
}

// Reload checks the current on-disk hash of the loaded GeoLite2 databases
// and will reload those files if they have changed.
// The function will return true if one (or both) databases were reloaded
// and any errors during the reload process. The current version of each
// database is only replaced if it could be successfully loaded.
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

	return changed, errors.Join(errs...)
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
