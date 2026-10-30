package safehttp

import "net/netip"

// TreatAsPublic makes c treat the given addresses as public, so that a test
// can reach an httptest server on loopback while every other rule holds.
func TreatAsPublic(c *Client, addrs ...netip.Addr) {
	for _, a := range addrs {
		c.g.treat[a.Unmap()] = true
	}
}

// Control is the Control hook of g's dialer.
func Control(g *Guard, address string) error { return g.control("tcp", address, nil) }
