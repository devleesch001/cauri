package api

import (
	"net/http"
	"net/netip"
	"strings"
)

// clientOf returns who sent r, to limit password guesses per client: the
// address of the connection, or, when that is a trusted proxy, the first
// address of X-Forwarded-For, read from the right, that is not one.
// Entries left of it were written by the client itself and are not
// trusted. IPv6 clients are their /64, which one usually holds whole.
func (s *server) clientOf(r *http.Request) netip.Prefix {
	addr := netip.Addr{}
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		addr = ap.Addr().Unmap()
	}
	if s.trusted(addr) {
		hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				break // malformed: the last trusted hop is all that is known
			}
			addr = hop.Unmap()
			if !s.trusted(addr) {
				break
			}
		}
	}
	if !addr.IsValid() {
		return netip.Prefix{}
	}
	bits := 32
	if addr.Is6() {
		bits = 64
	}
	p, _ := addr.Prefix(bits)
	return p
}

func (s *server) trusted(a netip.Addr) bool {
	for _, p := range s.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
