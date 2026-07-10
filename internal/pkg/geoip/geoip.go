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

type DB struct {
	asnMu  sync.RWMutex
	cityMu sync.RWMutex

	asn      *geoip2.ASNReader
	asnHash  string
	asnPath  string
	city     *geoip2.CityReader
	cityHash string
	cityPath string
}

func Open(asnpath, citypath string) (*DB, error) {
	db := &DB{
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

type AsnResult struct {
	Number uint `maxminddb:"autonomous_system_number"`
}

type CityResult struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Continent struct {
		Code string `maxminddb:"code"`
	} `maxminddb:"continent"`
}

func (d *DB) Lookup(ip net.IP) (*geoip2.ASN, *geoip2.CityResult, error) {
	errs := make([]error, 0)

	asn, err := d.lookupASN(ip)
	if err != nil {
		errs = append(errs, err)
		asn = &geoip2.ASN{}
	}

	city, err := d.lookupCity(ip)
	if err != nil {
		errs = append(errs, err)
		city = &geoip2.CityResult{}
	}

	return asn, city, errors.Join(errs...)
}

func (d *DB) lookupASN(ip net.IP) (*geoip2.ASN, error) {
	if d.asn == nil {
		return nil, fmt.Errorf("no ASN database loaded")
	}

	d.asnMu.RLock()
	defer d.asnMu.RUnlock()

	return d.asn.Lookup(ip)
}

func (d *DB) lookupCity(ip net.IP) (*geoip2.CityResult, error) {
	if d.city == nil {
		return nil, fmt.Errorf("no City database loaded")
	}

	d.cityMu.RLock()
	defer d.cityMu.RUnlock()

	return d.city.Lookup(ip)
}

func (d *DB) Reload() (bool, error) {
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

func (d *DB) reloadASN() (bool, error) {
	if d.asnPath == "" {
		return false, nil
	}

	hash, err := hashFile(d.asnPath)
	if err != nil {
		return false, fmt.Errorf("could not hash asn database: %w")
	}

	d.asnMu.RLock()
	nochange := d.asnHash == hash
	d.asnMu.RUnlock()

	if nochange {
		return false, nil
	}

	data, err := geoip2.NewASNReaderFromFile(d.asnPath)
	if err != nil {
		return false, fmt.Errorf("could not load new asn database: %w")
	}

	d.asnMu.Lock()
	d.asn = data
	d.asnHash = hash
	d.asnMu.Unlock()

	return true, nil
}

func (d *DB) reloadCity() (bool, error) {
	if d.cityPath == "" {
		return false, nil
	}

	hash, err := hashFile(d.cityPath)
	if err != nil {
		return false, fmt.Errorf("could not hash city database: %w")
	}

	d.cityMu.RLock()
	nochange := d.cityHash == hash
	d.cityMu.RUnlock()

	if nochange {
		return false, nil
	}

	data, err := geoip2.NewCityReaderFromFile(d.cityPath)
	if err != nil {
		return false, fmt.Errorf("could not load new city database: %w")
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
	defer f.Close()

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
