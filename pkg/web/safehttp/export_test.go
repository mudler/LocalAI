package safehttp

import "net/netip"

// TreatAsPublic makes c treat the given addresses as public, so that a test
// can reach an httptest server on loopback while every other rule holds.
func TreatAsPublic(c *Client, addrs ...netip.Addr) {
	allow := map[netip.Addr]bool{}
	for _, a := range addrs {
		allow[a] = true
	}
	base := c.allowed
	c.allowed = func(a netip.Addr) bool { return allow[a.Unmap()] || base(a) }
}
