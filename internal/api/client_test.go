package api

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientOf(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::1/128")}
	for _, c := range []struct {
		name    string
		trusted []netip.Prefix
		remote  string
		xff     []string
		want    string
	}{
		{"direct", nil, "203.0.113.7:4242", nil, "203.0.113.7/32"},
		{"header ignored without trusted proxies", nil, "203.0.113.7:4242", []string{"198.51.100.1"}, "203.0.113.7/32"},
		{"header ignored from an untrusted address", proxies, "203.0.113.7:4242", []string{"198.51.100.1"}, "203.0.113.7/32"},
		{"behind a trusted proxy", proxies, "10.1.2.3:4242", []string{"198.51.100.1"}, "198.51.100.1/32"},
		// The client may write anything left of what the proxy appends.
		{"forged entries left of the client", proxies, "10.1.2.3:4242", []string{"1.1.1.1, 198.51.100.1"}, "198.51.100.1/32"},
		{"chain of trusted proxies", proxies, "10.1.2.3:4242", []string{"198.51.100.1, 10.9.9.9"}, "198.51.100.1/32"},
		{"several headers", proxies, "10.1.2.3:4242", []string{"1.1.1.1", "198.51.100.1"}, "198.51.100.1/32"},
		{"malformed entry", proxies, "10.1.2.3:4242", []string{"198.51.100.1, junk"}, "10.1.2.3/32"},
		{"proxy without the header", proxies, "10.1.2.3:4242", nil, "10.1.2.3/32"},
		{"IPv6 grouped by /64", nil, "[2001:db8:1:2:3:4:5:6]:4242", nil, "2001:db8:1:2::/64"},
		{"IPv6 proxy", proxies, "[fd00::1]:4242", []string{"2001:db8::9"}, "2001:db8::/64"},
		{"IPv4 mapped in IPv6", nil, "[::ffff:203.0.113.7]:4242", nil, "203.0.113.7/32"},
	} {
		s := &server{Config: Config{TrustedProxies: c.trusted}}
		r := httptest.NewRequest("POST", "/api/admin/login", nil)
		r.RemoteAddr = c.remote
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := s.clientOf(r).String(); got != c.want {
			t.Errorf("%s: client = %s, want %s", c.name, got, c.want)
		}
	}
}
