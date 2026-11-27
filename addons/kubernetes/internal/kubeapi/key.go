// SPDX-License-Identifier: MIT

// Package kubeapi turns the credential of a connection into a client of the
// Kubernetes API, and holds what the tools share: the connection key, the
// output caps and the mapping of API errors.
package kubeapi

import (
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxKey is the longest connection key. The host keeps a resource of a
// binding to 200 characters.
const MaxKey = 200

const contextMark = "#context="

// ErrBadKey means a connection key is not <https server>#context=<name>.
var ErrBadKey = errors.New("the cluster must be written https://host:port#context=name")

// Conn is the cluster a connection names: the server URL and the context of
// the kubeconfig that was pinned when the connection was saved.
type Conn struct {
	Server  string
	Context string
}

// Key is the resource key of the connection.
func (c Conn) Key() string { return c.Server + contextMark + c.Context }

// ParseKey reads a resource key.
func ParseKey(key string) (Conn, error) {
	if utf8.RuneCountInString(key) > MaxKey {
		return Conn{}, ErrBadKey
	}
	server, name, ok := strings.Cut(key, contextMark)
	if !ok || !validContext(name) {
		return Conn{}, ErrBadKey
	}
	s, err := NormalizeServer(server)
	if err != nil {
		return Conn{}, ErrBadKey
	}
	return Conn{Server: s, Context: name}, nil
}

func validContext(name string) bool {
	if name == "" || len(name) > 253 || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		// The host refuses a resource with a space or a control character.
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// NormalizeServer checks a server URL and writes it one way: https, host in
// lower case without trailing dots, an IP address in its canonical form (an
// IPv4 address mapped into IPv6 written as IPv4), the port read as a number
// and left out when it is 443, and no user, path, query or fragment. The
// policy of a cluster is keyed by this form, so a kubeconfig that writes the
// same server another way still finds it. Writing the result again gives the
// same result.
//
// An IP address that is loopback, link-local, unspecified, multicast or a
// cloud metadata endpoint is refused: the credential would go to a service of
// the host or of its network. Host names are not resolved here; the client
// checks every address it dials (see dial.go). An internationalized host name
// is not converted to punycode: a server written in Unicode and the same
// server in punycode are two keys.
func NormalizeServer(s string) (string, error) {
	// A bare ? or # leaves no query or fragment in the parsed URL, but the
	// page reads it as one and refuses it; the add-on refuses it too, so
	// both write a server the same way.
	if strings.ContainsAny(s, "?#") {
		return "", errNotHTTPS
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errNotHTTPS
	}
	if u.Path != "" && u.Path != "/" {
		return "", errNotHTTPS
	}
	host := strings.TrimRight(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if host == "" || (port == "" && strings.HasSuffix(u.Host, ":")) {
		return "", errNotHTTPS
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return "", errNotHTTPS
		}
		ip = ip.Unmap()
		if blocked(ip) {
			return "", ErrLocalServer
		}
		host = ip.String()
	}
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return "", errNotHTTPS
		}
		port = strconv.FormatUint(n, 10)
	}
	if port == "443" {
		port = ""
	}
	hp := host
	if strings.Contains(host, ":") {
		hp = "[" + host + "]"
	}
	if port != "" {
		hp += ":" + port
	}
	return "https://" + hp, nil
}

// localAddr reports an address of the host or of its local link: loopback,
// link-local, unspecified or multicast.
func localAddr(ip netip.Addr) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

var errNotHTTPS = errors.New("the server must be an https URL with no path")

// ErrLocalServer means the server is an address of the host or of its local
// link.
var ErrLocalServer = errors.New("the server must not be a loopback, link-local, unspecified, multicast or cloud metadata address")
