package geoip_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/andrewheberle/geoip-spoa/internal/pkg/geoip"
)

const (
	testASN  = "../../../testdata/GeoLite2-ASN-Test.mmdb"
	testCity = "../../../testdata/GeoLite2-City-Test.mmdb"
)

var lookupTests = []struct {
	name string
	ip   string
	want geoip.Location
}{
	{"found both", "89.160.20.112", geoip.Location{ASN: 29518, Org: "Bredband2 AB", Continent: "EU", Country: "SE", City: "Linköping"}},
	{"both not found", "1.2.3.4", geoip.Location{}},
	{"only got city", "2.125.160.217", geoip.Location{Continent: "EU", Country: "GB", City: "Boxford"}},
	{"only got asn", "1.128.0.1", geoip.Location{ASN: 1221, Org: "Telstra Pty Ltd"}},
	{"country without city", "2a02:ff40::", geoip.Location{Continent: "EU", Country: "IM"}},
}

func open(t testing.TB, asn, city string, opts ...geoip.Option) *geoip.GeoDB {
	t.Helper()
	db, err := geoip.Open(asn, city, opts...)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return db
}

func runLookupTests(t *testing.T, db geoip.DB) {
	t.Helper()
	for _, tt := range lookupTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := db.Lookup(net.ParseIP(tt.ip))
			if err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Lookup() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGeoDB_Lookup(t *testing.T) {
	runLookupTests(t, open(t, testASN, testCity))
}

func TestCachingDB_Lookup(t *testing.T) {
	cache, err := geoip.NewCachingDB(open(t, testASN, testCity), 10, time.Minute)
	if err != nil {
		t.Fatalf("NewCachingDB() error = %v", err)
	}

	// run twice so the second pass is served from the cache
	runLookupTests(t, cache)
	runLookupTests(t, cache)
}

func TestGeoDB_Lookup_Locale(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	db := open(t, testASN, testCity, geoip.WithLocale("xx"), geoip.WithLogger(logger))

	got, err := db.Lookup(net.ParseIP("89.160.20.112"))
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	want := geoip.Location{ASN: 29518, Org: "Bredband2 AB", Continent: "EU", Country: "SE"}
	if got != want {
		t.Errorf("Lookup() = %+v, want %+v", got, want)
	}
	if !strings.Contains(buf.String(), "city name for chosen locale was not available") {
		t.Errorf("expected locale warning, got log %q", buf.String())
	}

	// no city record at all: no warning
	buf.Reset()
	if _, err := db.Lookup(net.ParseIP("2a02:ff40::")); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output, got %q", buf.String())
	}

	// other locales are available
	de := open(t, testASN, testCity, geoip.WithLocale("de"))
	got, err = de.Lookup(net.ParseIP("81.2.69.160"))
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if got.City != "London" {
		t.Errorf("Lookup() city = %q, want %q", got.City, "London")
	}
}

func TestGeoDB_Lookup_NoCityDatabase(t *testing.T) {
	db := open(t, testASN, "")

	got, err := db.Lookup(net.ParseIP("89.160.20.112"))
	if err == nil {
		t.Fatal("Lookup() succeeded unexpectedly")
	}
	want := geoip.Location{ASN: 29518, Org: "Bredband2 AB"}
	if got != want {
		t.Errorf("Lookup() = %+v, want %+v", got, want)
	}
}

// countingDB is an in-memory DB adapter that counts lookups.
type countingDB struct {
	loc   geoip.Location
	err   error
	calls int
}

func (c *countingDB) Lookup(net.IP) (geoip.Location, error) {
	c.calls++
	return c.loc, c.err
}

func (c *countingDB) Reload() (bool, error) { return false, nil }

func TestCachingDB_Lookup_DoesNotCacheErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCalls int
	}{
		{"success is cached", nil, 1},
		{"error is not cached", errors.New("boom"), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &countingDB{loc: geoip.Location{Country: "AU"}, err: tt.err}
			cache, err := geoip.NewCachingDB(inner, 10, 0)
			if err != nil {
				t.Fatalf("NewCachingDB() error = %v", err)
			}

			ip := net.ParseIP("192.0.2.1")
			for range 2 {
				if _, err := cache.Lookup(ip); !errors.Is(err, tt.err) {
					t.Fatalf("Lookup() error = %v, want %v", err, tt.err)
				}
			}
			if inner.calls != tt.wantCalls {
				t.Errorf("underlying Lookup calls = %d, want %d", inner.calls, tt.wantCalls)
			}
		})
	}
}

func BenchmarkLookup_Uncached(b *testing.B) {
	db := open(b, testASN, testCity)
	ip := net.ParseIP("8.8.8.8")

	b.ResetTimer()
	for b.Loop() {
		_, _ = db.Lookup(ip)
	}
}

func BenchmarkLookup_Cached(b *testing.B) {
	cache, err := geoip.NewCachingDB(open(b, testASN, testCity), 100, time.Minute)
	if err != nil {
		b.Fatalf("NewCachingDB() error = %v", err)
	}
	ip := net.ParseIP("8.8.8.8")

	b.ResetTimer()
	for b.Loop() {
		_, _ = cache.Lookup(ip)
	}
}
