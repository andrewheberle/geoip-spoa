package geoip_test

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
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

func TestGeoDB_Lookup_Cached(t *testing.T) {
	db := open(t, testASN, testCity, geoip.WithCache(10, time.Minute))

	// run twice so the second pass is served from the cache
	runLookupTests(t, db)
	runLookupTests(t, db)
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

// reloadFixture holds temporary copies of the test databases that a test can
// overwrite to simulate new database versions.
type reloadFixture struct {
	asnPath, cityPath  string
	asnEdited, asnOrig []byte
	cityOrig           []byte
}

const (
	reloadIP    = "89.160.20.112"
	origOrg     = "Bredband2 AB"
	editedOrg   = "Bredband3 AB"
	corruptData = "not a maxmind database"
)

func newReloadFixture(t *testing.T) *reloadFixture {
	t.Helper()

	asn, err := os.ReadFile(testASN)
	if err != nil {
		t.Fatal(err)
	}
	city, err := os.ReadFile(testCity)
	if err != nil {
		t.Fatal(err)
	}

	// same-length edit keeps the database valid but changes its contents
	edited := bytes.Replace(asn, []byte(origOrg), []byte(editedOrg), 1)
	if bytes.Equal(edited, asn) {
		t.Fatalf("test ASN database does not contain %q", origOrg)
	}

	dir := t.TempDir()
	f := &reloadFixture{
		asnPath:   filepath.Join(dir, "asn.mmdb"),
		cityPath:  filepath.Join(dir, "city.mmdb"),
		asnOrig:   asn,
		asnEdited: edited,
		cityOrig:  city,
	}
	f.write(t, f.asnPath, asn)
	f.write(t, f.cityPath, city)

	return f
}

func (f *reloadFixture) write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func lookupOrg(t *testing.T, db geoip.DB) string {
	t.Helper()
	loc, err := db.Lookup(net.ParseIP(reloadIP))
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	return loc.Org
}

func TestGeoDB_Reload(t *testing.T) {
	tests := []struct {
		name        string
		asn, city   func(f *reloadFixture) []byte
		wantChanged bool
		wantErr     bool
		wantOrg     string
	}{
		{
			name:    "unchanged",
			wantOrg: origOrg,
		},
		{
			name:        "changed",
			asn:         func(f *reloadFixture) []byte { return f.asnEdited },
			wantChanged: true,
			wantOrg:     editedOrg,
		},
		{
			name:    "corrupt keeps old data",
			asn:     func(*reloadFixture) []byte { return []byte(corruptData) },
			wantErr: true,
			wantOrg: origOrg,
		},
		{
			name:        "one changed and one corrupt",
			asn:         func(f *reloadFixture) []byte { return f.asnEdited },
			city:        func(*reloadFixture) []byte { return []byte(corruptData) },
			wantChanged: true,
			wantErr:     true,
			wantOrg:     editedOrg,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReloadFixture(t)
			db := open(t, f.asnPath, f.cityPath, geoip.WithCache(10, 0))

			// populate the cache
			if got := lookupOrg(t, db); got != origOrg {
				t.Fatalf("Lookup() org = %q, want %q", got, origOrg)
			}

			if tt.asn != nil {
				f.write(t, f.asnPath, tt.asn(f))
			}
			if tt.city != nil {
				f.write(t, f.cityPath, tt.city(f))
			}

			changed, err := db.Reload()
			if changed != tt.wantChanged {
				t.Errorf("Reload() changed = %v, want %v", changed, tt.wantChanged)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("Reload() error = %v, wantErr %v", err, tt.wantErr)
			}

			// a cached result that survived a change would return the old org
			if got := lookupOrg(t, db); got != tt.wantOrg {
				t.Errorf("Lookup() org = %q, want %q", got, tt.wantOrg)
			}
		})
	}
}

func TestGeoDB_Run(t *testing.T) {
	f := newReloadFixture(t)
	db := open(t, f.asnPath, f.cityPath, geoip.WithCache(10, 0), geoip.WithReloadInterval(10*time.Millisecond))

	if got := lookupOrg(t, db); got != origOrg {
		t.Fatalf("Lookup() org = %q, want %q", got, origOrg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- db.Run(ctx)
	}()

	f.write(t, f.asnPath, f.asnEdited)

	deadline := time.Now().Add(5 * time.Second)
	for lookupOrg(t, db) != editedOrg {
		if time.Now().After(deadline) {
			t.Fatal("Run() did not load the changed database")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after cancel")
	}
}

func TestGeoDB_Run_Disabled(t *testing.T) {
	db := open(t, testASN, testCity)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- db.Run(ctx)
	}()

	select {
	case err := <-done:
		t.Fatalf("Run() returned %v before cancel", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after cancel")
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
	db := open(b, testASN, testCity, geoip.WithCache(100, time.Minute))
	ip := net.ParseIP("8.8.8.8")

	b.ResetTimer()
	for b.Loop() {
		_, _ = db.Lookup(ip)
	}
}
