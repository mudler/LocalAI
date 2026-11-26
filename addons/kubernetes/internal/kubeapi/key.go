// SPDX-License-Identifier: MIT

// Package kubeapi turns the credential of a connection into a client of the
// Kubernetes API, and holds what the tools share: the connection key, the
// output caps and the mapping of API errors.
package kubeapi

import (
	"errors"
	"fmt"
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
// lower case, no user, path, query or fragment. The port stays as written.
func NormalizeServer(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("the server must be an https URL with no path")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("the server must be an https URL with no path")
	}
	return "https://" + strings.ToLower(u.Host), nil
}
