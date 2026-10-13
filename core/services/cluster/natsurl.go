package cluster

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// ErrCredentialInURL means an address carries a user name, a password or a query
// string. The cluster settings are shared and readable by every admin, so no
// credential is stored in them: a replica keeps its keys and tokens in its own
// flags and files.
var ErrCredentialInURL = errors.New("the address must not carry a user name, a password or a query string: " +
	"credentials stay on each replica (flags, environment or files)")

// CheckNATSURL says whether raw is an address that may be stored for the
// cluster: a NATS, TLS or WebSocket address with a host, and with no user name,
// no password, no query string and no fragment.
//
// The NATS client accepts several servers in one string, separated by commas.
// Each one is checked.
func CheckNATSURL(raw string) error {
	for _, one := range strings.Split(raw, ",") {
		if err := checkOneNATSURL(strings.TrimSpace(one)); err != nil {
			return err
		}
	}
	return nil
}

func checkOneNATSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a URL: %w", err)
	}
	if !slices.Contains([]string{"nats", "tls", "ws", "wss"}, u.Scheme) {
		return fmt.Errorf("the scheme must be nats, tls, ws or wss, not %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("the address has no host")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return ErrCredentialInURL
	}
	return nil
}

// PublicNATSURL returns raw without its user name, password, query string and
// fragment. Use it for every view and log line of an address that may come from
// a flag. It returns "***" for text that is not a URL.
func PublicNATSURL(raw string) string {
	parts := strings.Split(raw, ",")
	for i, one := range parts {
		parts[i] = publicOneNATSURL(strings.TrimSpace(one))
	}
	return strings.Join(parts, ",")
}

func publicOneNATSURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "***"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}
