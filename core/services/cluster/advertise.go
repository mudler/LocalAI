package cluster

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// DiscoverAdvertisedAddr determines the address this replica should advertise
// to its peers, with no operator configuration.
//
// Every replica in a deployment reaches the same PostgreSQL server, so the
// local interface that routes to PostgreSQL is on a network all the replicas
// demonstrably share. Opening a UDP socket toward the database sends no packet;
// it only asks the kernel to pick a source address for that route, which is the
// address to advertise. The caller supplies the port, since the frontend's
// listening port has nothing to do with the database's.
//
// What defeats the discovery is a DSN that NAMES loopback, not the database
// being co-located. Co-location is fine as long as the DSN names something
// routable: compose's usual `host=postgres` resolves to a bridge address, so
// the kernel picks this container's own bridge IP as the source, which is the
// address a peer on that network dials. It is `host=localhost` (or 127.0.0.1,
// or ::1) that makes the route loopback, and advertising 127.0.0.1 would make
// a peer dialling this replica reach itself instead. So an unspecified,
// loopback, or scoped source address is rejected with an error telling the
// operator to configure the advertised address explicitly, rather than
// returned. There is no fallback string: no address is better than a wrong one.
func DiscoverAdvertisedAddr(dsn string, port int) (string, error) {
	// A port of 0 (or out of range) would produce an address nothing can dial,
	// and the caller is likelier to have passed an unset field than to mean it.
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("advertised port %d is out of range 1-65535", port)
	}
	host, dbPort, err := dsnHostPort(dsn)
	if err != nil {
		return "", err
	}
	conn, err := net.Dial("udp", net.JoinHostPort(host, dbPort))
	if err != nil {
		return "", fmt.Errorf("resolving route to database host %q: %w", host, err)
	}
	// Nothing was ever sent on this socket, so a close failure carries no
	// information about the address we just read.
	defer func() { _ = conn.Close() }()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || local.IP == nil {
		return "", fmt.Errorf("no local address on the route to database host %q; set the advertised address explicitly", host)
	}
	if reason := unroutableReason(local.IP, local.Zone); reason != "" {
		return "", fmt.Errorf("the route to database host %q is %s; set the advertised address explicitly", host, reason)
	}
	return net.JoinHostPort(local.IP.String(), strconv.Itoa(port)), nil
}

// unroutableReason says why ip cannot serve as an address other hosts dial, or
// "" when it can. It is the one place that decides, so the discovered address
// and the configured one are held to the same rule; they differ only in what
// they do with the answer.
func unroutableReason(ip net.IP, zone string) string {
	switch {
	case ip == nil || ip.IsUnspecified():
		return fmt.Sprintf("unspecified (%s), which is a bind address rather than one anything can connect to", ip)
	case ip.IsLoopback():
		return fmt.Sprintf("loopback (%s), which means \"this host\" to whoever dials it, so every peer would reach itself", ip)
	case ip.IsLinkLocalUnicast():
		return fmt.Sprintf("link-local (%s), which peers on other hosts cannot dial", withZone(ip, zone))
	// A zone is normally attached only to a link-local address, which the case
	// above already rejects. This one stays for the scoped address of some
	// other class a platform may hand back, and says so rather than repeating
	// the link-local label: the two have different cures, and an operator told
	// the wrong one looks in the wrong place.
	case zone != "":
		return fmt.Sprintf("scoped to interface %q (%s), and the zone is dropped by the time an address is stored, leaving a host nothing can dial", zone, withZone(ip, zone))
	}
	return ""
}

// withZone renders the address the way it has to be dialled. IP.String() drops
// the %iface, so an unadorned %s in a rejection reports an address that differs
// from the one being rejected.
func withZone(ip net.IP, zone string) string {
	if zone == "" {
		return ip.String()
	}
	return ip.String() + "%" + zone
}

// CheckAdvertisedAddr validates an address an operator configured, returning a
// reason it is questionable, or an error if it is unusable.
//
// A configured address bypasses every check DiscoverAdvertisedAddr performs,
// and the value most likely to be copied is the one that works on a single
// host: "127.0.0.1:8080" on three hosts makes every peer dial itself, which
// presents as a relay loop rather than as a configuration error.
//
// The split between error and reason is deliberate. An address that cannot be
// parsed into host and port is an error, because nothing can dial it at all. An
// address that merely means "this host" is a reason to warn and no more: a
// single-host deployment, including this repository's own e2e cluster, uses one
// correctly, and refusing it would be refusing a supported topology.
func CheckAdvertisedAddr(addr string) (reason string, err error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("advertised address %q is not host:port: %w", addr, err)
	}
	if host == "" {
		return "", fmt.Errorf("advertised address %q names no host, so peers have nothing to dial", addr)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("advertised address %q has no usable port (want 1-65535)", addr)
	}
	// The zone is split off before parsing because net.ParseIP rejects
	// "fe80::1%eth0" outright. Left joined, a scoped literal would look like a
	// name and collect no warning at all, which is the one case where the
	// address is guaranteed not to work for a peer.
	host, zone := splitZone(host)
	// A name is resolved by whoever dials it, and may resolve differently
	// there, so its presence is all this side can check.
	ip := net.ParseIP(host)
	if ip == nil {
		return "", nil
	}
	return unroutableReason(ip, zone), nil
}

// splitZone separates an IPv6 scope from the address it qualifies. A name
// never carries one, so a host with no "%" comes back unchanged.
func splitZone(host string) (string, string) {
	addr, zone, found := strings.Cut(host, "%")
	if !found {
		return host, ""
	}
	return addr, zone
}

// dsnHostPort extracts the host and port from either DSN form gorm's postgres
// driver accepts: a URL ("postgres://user:pass@host:5432/db") or libpq keyword
// pairs ("host=... port=...").
func dsnHostPort(dsn string) (string, string, error) {
	const defaultPort = "5432"
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return "", "", errors.New("empty database DSN")
	}

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", "", fmt.Errorf("parsing database DSN: %w", err)
		}
		host := u.Hostname()
		if host == "" {
			return "", "", errors.New("database DSN has no host")
		}
		port := u.Port()
		if port == "" {
			port = defaultPort
		}
		return host, port, nil
	}

	host, port := "", defaultPort
	for _, field := range strings.Fields(dsn) {
		key, value, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		switch key {
		case "host":
			host = value
		case "port":
			port = value
		}
	}
	if host == "" {
		return "", "", errors.New("database DSN has no host")
	}
	// A Unix socket directory tells us nothing about which interface reaches
	// the database, so there is no address to derive.
	if strings.HasPrefix(host, "/") {
		return "", "", fmt.Errorf("database DSN uses a unix socket (%q); no routable address to advertise", host)
	}
	return host, port, nil
}
