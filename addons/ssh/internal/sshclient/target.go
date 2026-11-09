// SPDX-License-Identifier: MIT
package sshclient

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// PinPrefix starts the host key pin in the address of a connection.
const PinPrefix = "sha256="

// DefaultPort is the SSH port used when the address names none.
const DefaultPort = "22"

// ErrBadAddress is the refusal of an address that does not parse.
var ErrBadAddress = errors.New("the address is not valid: use user@host or user@host:port, with an optional #sha256=<fingerprint>; write an IPv6 address in brackets")

var (
	userRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._\\+-]{0,63}$`)
	hostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$`)
	hexRE  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Target is the one machine of a connection: who to log in as, where, and the
// host key to accept.
type Target struct {
	User string
	Host string
	Port string
	// Pin is the lower-case hex SHA-256 of the host key, or "".
	Pin string
}

// ParseTarget reads "user@host[:port][#sha256=<hex>]". The host is lower-cased
// and an IPv6 address is written in brackets.
func ParseTarget(s string) (Target, error) {
	s = strings.TrimSpace(s)
	base, frag, hasFrag := strings.Cut(s, "#")
	var t Target
	if hasFrag {
		hexPin, ok := strings.CutPrefix(frag, PinPrefix)
		if !ok {
			return t, ErrBadAddress
		}
		hexPin = strings.ToLower(strings.ReplaceAll(hexPin, ":", ""))
		if !hexRE.MatchString(hexPin) {
			return t, ErrBadAddress
		}
		t.Pin = hexPin
	}
	at := strings.LastIndex(base, "@")
	if at < 1 {
		return t, ErrBadAddress
	}
	t.User = base[:at]
	if !userRE.MatchString(t.User) {
		return t, ErrBadAddress
	}
	host, port := base[at+1:], DefaultPort
	switch {
	case strings.HasPrefix(host, "["):
		h, p, err := net.SplitHostPort(host)
		if err != nil {
			// "[::1]" with no port.
			if !strings.HasSuffix(host, "]") {
				return t, ErrBadAddress
			}
			h = host[1 : len(host)-1]
		} else {
			port = p
		}
		ip, err := netip.ParseAddr(h)
		if err != nil || !ip.Is6() {
			return t, ErrBadAddress
		}
		t.Host = ip.String()
	case strings.Count(host, ":") == 1:
		h, p, _ := strings.Cut(host, ":")
		host, port = h, p
		fallthrough
	default:
		host = strings.ToLower(host)
		if ip, err := netip.ParseAddr(host); err == nil {
			if !ip.Is4() {
				return t, ErrBadAddress
			}
			host = ip.String()
		} else if !hostRE.MatchString(host) {
			return t, ErrBadAddress
		}
		t.Host = host
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return t, ErrBadAddress
	}
	t.Port = port
	return t, nil
}

// HostPort is the address to dial.
func (t Target) HostPort() string { return net.JoinHostPort(t.Host, t.Port) }

// Key is the address without the pin, in one canonical form.
func (t Target) Key() string { return t.User + "@" + t.HostPort() }

// PinHex returns the pin of a host key: the lower-case hex SHA-256 of its wire form.
func PinHex(k ssh.PublicKey) string {
	sum := sha256.Sum256(k.Marshal())
	return hex.EncodeToString(sum[:])
}

// FingerprintOfPin writes a pin the way ssh-keygen -l does: SHA256: and the
// unpadded base64 of the digest.
func FingerprintOfPin(pin string) string {
	b, err := hex.DecodeString(pin)
	if err != nil {
		return ""
	}
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(b)
}
