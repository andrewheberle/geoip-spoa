package geoip_test

import (
	"net"
	"testing"
	"time"

	"github.com/andrewheberle/geoip-spoa/internal/pkg/geoip"
)

func TestGeoDB_Lookup(t *testing.T) {
	db, err := geoip.Open("../../../testdata/GeoLite2-ASN-Test.mmdb", "../../../testdata/GeoLite2-City-Test.mmdb")
	if err != nil {
		panic(err)
	}

	tests := []struct {
		name     string
		ip       net.IP
		wantAsn  uint32
		wantCity string
		wantErr  bool
	}{
		{"found both", net.ParseIP("89.160.20.112"), 29518, "Linköping", false},
		{"both not found", net.ParseIP("1.2.3.4"), 0, "", true},
		{"only got city", net.ParseIP("2.125.160.217"), 0, "Boxford", true},
		{"only got asn", net.ParseIP("1.128.0.1"), 1221, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err != nil {
				t.Fatalf("could not construct receiver type: %v", err)
			}
			asn, city, gotErr := db.Lookup(tt.ip)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Lookup() failed: %v", gotErr)
				}
			} else {
				if tt.wantErr {
					t.Fatal("Lookup() succeeded unexpectedly")
				}
			}

			if asn.AutonomousSystemNumber != tt.wantAsn {
				t.Errorf("Lookup() = asn %v, want %v", asn.AutonomousSystemNumber, tt.wantAsn)
			}
			name, ok := city.City.Names["en"]
			if !ok {
				name = ""
			}
			if name != tt.wantCity {
				t.Errorf("Lookup() = city %v, want %v", name, tt.wantCity)
			}
		})
	}
}

func TestCachingDB_Lookup(t *testing.T) {
	db, err := geoip.Open("../../../testdata/GeoLite2-ASN-Test.mmdb", "../../../testdata/GeoLite2-City-Test.mmdb")
	if err != nil {
		panic(err)
	}
	cache, err := geoip.NewCachingDB(db, 10, time.Second*60)
	if err != nil {
		panic(err)
	}

	tests := []struct {
		name     string
		ip       net.IP
		wantAsn  uint32
		wantCity string
		wantErr  bool
	}{
		{"found both", net.ParseIP("89.160.20.112"), 29518, "Linköping", false},
		{"both not found", net.ParseIP("1.2.3.4"), 0, "", true},
		{"only got city", net.ParseIP("2.125.160.217"), 0, "Boxford", true},
		{"only got asn", net.ParseIP("1.128.0.1"), 1221, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err != nil {
				t.Fatalf("could not construct receiver type: %v", err)
			}
			asn, city, gotErr := cache.Lookup(tt.ip)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Lookup() failed: %v", gotErr)
				}
			} else {
				if tt.wantErr {
					t.Fatal("Lookup() succeeded unexpectedly")
				}
			}

			if asn.AutonomousSystemNumber != tt.wantAsn {
				t.Errorf("Lookup() = asn %v, want %v", asn.AutonomousSystemNumber, tt.wantAsn)
			}
			name, ok := city.City.Names["en"]
			if !ok {
				name = ""
			}
			if name != tt.wantCity {
				t.Errorf("Lookup() = city %v, want %v", name, tt.wantCity)
			}
		})
	}
}

func BenchmarkLookup_Uncached(b *testing.B) {
	db, err := geoip.Open("../../../testdata/GeoLite2-ASN-Test.mmdb", "../../../testdata/GeoLite2-City-Test.mmdb")
	if err != nil {
		panic(err)
	}
	ip := net.ParseIP("8.8.8.8")

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = db.Lookup(ip)
	}
}

func BenchmarkLookup_Cached(b *testing.B) {
	db, err := geoip.Open("../../../testdata/GeoLite2-ASN-Test.mmdb", "../../../testdata/GeoLite2-City-Test.mmdb")
	if err != nil {
		panic(err)
	}
	cache, err := geoip.NewCachingDB(db, 100, time.Minute)
	if err != nil {
		b.Fatalf("NewCachingDB() error = %v", err)
	}
	ip := net.ParseIP("8.8.8.8")

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = cache.Lookup(ip)
	}
}
