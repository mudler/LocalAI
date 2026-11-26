// SPDX-License-Identifier: MIT

// Package kubeapi turns the credential of a connection into a client of the
// Kubernetes API, and holds what the tools share: the connection key, the
// output caps and the mapping of API errors.
package kubeapi

import (
	"errors"
	"net/netip"
	"net/url"
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
// lower case without a trailing dot, the default port 443 left out, and no
// user, path, query or fragment. The policy of a cluster is keyed by this
// form, so a kubeconfig that writes the same server another way still finds
// it.
//
// An IP address that is loopback, link-local, unspecified or multicast is
// refused: the credential would go to a service of the host or of its
// network, such as a cloud metadata endpoint. Host names are not resolved.
func NormalizeServer(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errNotHTTPS
	}
	if u.Path != "" && u.Path != "/" {
		return "", errNotHTTPS
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if host == "" || (port == "" && strings.HasSuffix(u.Host, ":")) {
		return "", errNotHTTPS
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return "", ErrLocalServer
		}
		if ip.Zone() != "" {
			return "", errNotHTTPS
		}
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

var errNotHTTPS = errors.New("the server must be an https URL with no path")

// ErrLocalServer means the server is an address of the host or of its local
// link.
var ErrLocalServer = errors.New("the server must not be a loopback, link-local, unspecified or multicast address")
