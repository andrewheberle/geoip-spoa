package spoa

import (
	"errors"
	"net"
	"testing"

	"github.com/andrewheberle/geoip-spoa/internal/pkg/geoip"
	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/payload/kv"
	"github.com/negasus/haproxy-spoe-go/request"
	"github.com/prometheus/client_golang/prometheus"
)

// fakeDB is an in-memory geoip.DB adapter returning a fixed result.
type fakeDB struct {
	loc geoip.Location
	err error
}

func (f fakeDB) Lookup(net.IP) (geoip.Location, error) { return f.loc, f.err }

// newRequest builds a request carrying a single message with the given name
// and, if ip is non-nil, an "ip" key holding it.
func newRequest(name string, ip any) *request.Request {
	req := request.AcquireRequest()

	msg := message.AcquireMessage()
	msg.Name = name
	msg.KV = kv.NewKV()
	if ip != nil {
		msg.KV.Add("ip", ip)
	}
	*req.Messages = append(*req.Messages, msg)

	return req
}

// statusCounts returns the number of observations per status label.
func statusCounts(t *testing.T, reg *prometheus.Registry) map[string]uint64 {
	t.Helper()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	counts := make(map[string]uint64)
	for _, f := range families {
		if f.GetName() != "geoip_agent_request_duration_seconds" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "status" {
					counts[l.GetValue()] += m.GetHistogram().GetSampleCount()
				}
			}
		}
	}

	return counts
}

func TestServer_handler(t *testing.T) {
	sydney := geoip.Location{ASN: 1221, Org: "Telstra Pty Ltd", Continent: "OC", Country: "AU", City: "Sydney"}

	tests := []struct {
		name       string
		db         fakeDB
		req        *request.Request
		wantVars   map[string]any
		wantStatus string
	}{
		{
			name: "hit",
			db:   fakeDB{loc: sydney},
			req:  newRequest(MessageName, net.ParseIP("1.128.0.1")),
			wantVars: map[string]any{
				"asn": uint32(1221), "org": "Telstra Pty Ltd", "continent": "OC", "country": "AU", "city": "Sydney",
			},
			wantStatus: "success",
		},
		{
			name: "not found",
			db:   fakeDB{},
			req:  newRequest(MessageName, net.ParseIP("192.0.2.1")),
			wantVars: map[string]any{
				"asn": uint32(0), "org": "", "continent": "", "country": "", "city": "",
			},
			wantStatus: "success",
		},
		{
			name: "db error keeps partial result",
			db:   fakeDB{loc: geoip.Location{ASN: 1221, Org: "Telstra Pty Ltd"}, err: errors.New("no City database loaded")},
			req:  newRequest(MessageName, net.ParseIP("1.128.0.1")),
			wantVars: map[string]any{
				"asn": uint32(1221), "org": "Telstra Pty Ltd", "continent": "", "country": "", "city": "",
			},
			wantStatus: "error",
		},
		{
			name:       "missing message",
			db:         fakeDB{loc: sydney},
			req:        newRequest("other-message", net.ParseIP("1.128.0.1")),
			wantStatus: "error",
		},
		{
			name:       "missing ip",
			db:         fakeDB{loc: sydney},
			req:        newRequest(MessageName, nil),
			wantStatus: "error",
		},
		{
			name:       "ip has wrong type",
			db:         fakeDB{loc: sydney},
			req:        newRequest(MessageName, "1.128.0.1"),
			wantStatus: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			s, err := NewServer("127.0.0.1:0", tt.db, WithRegistry(reg))
			if err != nil {
				t.Fatalf("NewServer() error = %v", err)
			}

			s.handler(tt.req)

			got := make(map[string]any)
			for _, a := range tt.req.Actions {
				got[a.Name] = a.Value
			}
			if len(got) != len(tt.wantVars) {
				t.Errorf("handler set %d vars %v, want %d %v", len(got), got, len(tt.wantVars), tt.wantVars)
			}
			for name, want := range tt.wantVars {
				if got[name] != want {
					t.Errorf("var %q = %#v, want %#v", name, got[name], want)
				}
			}

			counts := statusCounts(t, reg)
			if counts[tt.wantStatus] != 1 || len(counts) != 1 {
				t.Errorf("status observations = %v, want one %q", counts, tt.wantStatus)
			}
		})
	}
}
