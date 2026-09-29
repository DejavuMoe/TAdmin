package feeds

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// blockedPrefixes lists special-purpose ranges not covered by the netip
// predicates used in isBlockedIP.
var blockedPrefixes = mustPrefixes(
	"0.0.0.0/8",       // "this network"
	"100.64.0.0/10",   // carrier-grade NAT
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"240.0.0.0/4",     // reserved, incl. limited broadcast
	"::/96",           // IPv4-compatible (deprecated)
	"100::/64",        // discard-only
	"2001:db8::/32",   // documentation
	"fec0::/10",       // site-local (deprecated)
)

var (
	nat64Prefix  = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour    = netip.MustParsePrefix("2002::/16")
	nat64Local   = netip.MustParsePrefix("64:ff9b:1::/48")
	errNoAddress = errors.New("no addresses")
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// isBlockedIP reports whether ip must not be dialed when private
// destinations are disallowed: loopback, private (RFC 1918 / ULA),
// link-local, unspecified, multicast, CGNAT and other special-purpose
// ranges, including IPv4 addresses embedded in IPv4-mapped, NAT64 and 6to4
// IPv6 addresses.
func isBlockedIP(ip netip.Addr) bool {
	ip = ip.WithZone("").Unmap()
	if !ip.IsValid() {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	if ip.Is6() {
		b := ip.As16()
		switch {
		case nat64Local.Contains(ip):
			return true
		case nat64Prefix.Contains(ip):
			return isBlockedIP(netip.AddrFrom4([4]byte(b[12:16])))
		case sixToFour.Contains(ip):
			return isBlockedIP(netip.AddrFrom4([4]byte(b[2:6])))
		}
	}
	return false
}

// safeDialer resolves host names itself, drops disallowed addresses and
// dials only the checked IPs, so DNS rebinding cannot slip a private
// address in between check and connect. A Control hook re-checks the
// socket address as defense in depth.
type safeDialer struct {
	dialer       *net.Dialer
	resolver     *net.Resolver
	allowPrivate bool
}

func newSafeDialer(allowPrivate bool) *safeDialer {
	d := &safeDialer{
		dialer:       &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second},
		resolver:     net.DefaultResolver,
		allowPrivate: allowPrivate,
	}
	if !allowPrivate {
		d.dialer.Control = controlBlocked
	}
	return d
}

func controlBlocked(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: unparsable dial address %q", ErrBlockedAddress, address)
	}
	if isBlockedIP(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, ap.Addr())
	}
	return nil
}

func (d *safeDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if d.allowPrivate {
		return d.dialer.DialContext(ctx, network, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, err = d.resolver.LookupNetIP(ctx, ipNetwork(network), host)
		if err != nil {
			return nil, err
		}
	}
	var allowed []netip.Addr
	for _, ip := range ips {
		if !isBlockedIP(ip) {
			allowed = append(allowed, ip)
		}
	}
	if len(allowed) == 0 {
		if len(ips) == 0 {
			return nil, fmt.Errorf("feeds: resolve %s: %w", host, errNoAddress)
		}
		return nil, fmt.Errorf("%w: %s resolves to %s", ErrBlockedAddress, host, ips[0])
	}
	var firstErr error
	for _, ip := range allowed {
		conn, err := d.dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, firstErr
}

func ipNetwork(network string) string {
	switch network {
	case "tcp4", "udp4":
		return "ip4"
	case "tcp6", "udp6":
		return "ip6"
	}
	return "ip"
}
