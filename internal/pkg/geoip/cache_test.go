package geoip

import (
	"errors"
	"net"
	"testing"
)

func TestLookupCache_DoesNotCacheErrors(t *testing.T) {
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
			calls := 0
			fn := func(net.IP) (Location, error) {
				calls++
				return Location{Country: "AU"}, tt.err
			}

			c := newLookupCache(10, 0)
			ip := net.ParseIP("192.0.2.1")
			for range 2 {
				if _, err := c.lookup(ip, fn); !errors.Is(err, tt.err) {
					t.Fatalf("lookup() error = %v, want %v", err, tt.err)
				}
			}
			if calls != tt.wantCalls {
				t.Errorf("underlying lookup calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestLookupCache_PurgeDuringLookupIsNotCached(t *testing.T) {
	c := newLookupCache(10, 0)
	ip := net.ParseIP("192.0.2.1")

	// a purge while the underlying lookup is in flight means the result may
	// come from the old databases, so it must not be cached
	calls := 0
	fn := func(net.IP) (Location, error) {
		calls++
		if calls == 1 {
			c.purge()
		}
		return Location{Country: "AU"}, nil
	}

	for range 2 {
		if _, err := c.lookup(ip, fn); err != nil {
			t.Fatalf("lookup() error = %v", err)
		}
	}
	if calls != 2 {
		t.Errorf("underlying lookup calls = %d, want 2", calls)
	}
}

func TestNewLookupCache_Disabled(t *testing.T) {
	for _, size := range []int{0, -1} {
		if c := newLookupCache(size, 0); c != nil {
			t.Errorf("newLookupCache(%d) = %v, want nil", size, c)
		}
	}
}
