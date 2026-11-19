// Package access evaluates per-route client address rules.
package access

import (
	"fmt"
	"net/netip"
)

// List is a compiled set of address rules. A nil List allows everyone.
type List struct {
	allow, deny []netip.Prefix
	denyAll     bool
}

// Parse compiles allow and deny entries, each an IP address or a CIDR range.
func Parse(allow, deny []string) (*List, error) {
	if len(allow) == 0 && len(deny) == 0 {
		return nil, nil
	}
	l := &List{}
	var err error
	if l.allow, err = parsePrefixes(allow); err != nil {
		return nil, err
	}
	if l.deny, err = parsePrefixes(deny); err != nil {
		return nil, err
	}
	return l, nil
}

// Compile is Parse for rules that already passed config validation. Should an
// entry be invalid anyway, the list fails closed.
func Compile(allow, deny []string) *List {
	l, err := Parse(allow, deny)
	if err != nil {
		return &List{denyAll: true}
	}
	return l
}

func parsePrefixes(entries []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		if p, err := netip.ParsePrefix(e); err == nil {
			prefixes = append(prefixes, unmapPrefix(p).Masked())
			continue
		}
		addr, err := netip.ParseAddr(e)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or CIDR range", e)
		}
		addr = addr.Unmap().WithZone("")
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

func unmapPrefix(p netip.Prefix) netip.Prefix {
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	return p
}

// AllowsRemote reports whether a "host:port" client address may use the route:
// deny entries win, and a non-empty allow list must contain the address.
// An address that cannot be parsed is refused.
func (l *List) AllowsRemote(remote string) bool {
	if l == nil {
		return true
	}
	if l.denyAll {
		return false
	}
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return false
	}
	addr := ap.Addr().Unmap().WithZone("")
	if contains(l.deny, addr) {
		return false
	}
	return len(l.allow) == 0 || contains(l.allow, addr)
}

func contains(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
