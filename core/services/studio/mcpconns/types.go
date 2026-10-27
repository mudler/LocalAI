// Package mcpconns keeps the MCP servers that a user defines for their chats,
// called connectors in the UI. A record is private to its owner: another user
// never reads, tests or uses it. Headers, tokens and the environment of a local
// command are sealed at rest and never leave the server.
//
// Two stores exist: NewJSON (files under a data directory, or memory) and
// NewGORM (SQLite or PostgreSQL). mcpconnstest.Run checks both.
package mcpconns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Transports of a connector.
const (
	// TransportHTTP is a remote server. Streamable HTTP is tried first and the
	// legacy SSE transport is the fallback.
	TransportHTTP = "http"
	// TransportStdio is a command run on the host of the frontend. Only an
	// administrator may use it.
	TransportStdio = "stdio"
)

// Limits.
const (
	MaxNameRunes  = 80
	MaxPerUser    = 20
	MaxHeaders    = 20
	MaxArgs       = 50
	MaxEnv        = 50
	MaxValueBytes = 4096
)

// Errors.
var (
	ErrNotFound  = errors.New("mcpconns: not found")
	ErrInvalid   = errors.New("mcpconns: invalid connector")
	ErrForbidden = errors.New("mcpconns: not allowed")
)

// Connector is one MCP server of one user.
type Connector struct {
	ID, Owner, Name, Transport string
	// URL is the address of a remote server.
	URL string
	// Command and Args start a local server (stdio).
	Command string
	Args    []string
	// Secrets is sealed JSON (see Secrets). In a Store it is sealed bytes; the
	// Store returns what it was given.
	Secrets []byte
	// HasToken, HasHeaders and HasEnv tell the API what is set without opening
	// the sealed value.
	HasToken, HasHeaders, HasEnv bool
	Enabled                      bool
	Created, Updated             time.Time
}

// Secrets is what is sealed.
type Secrets struct {
	Token   string            `json:"token,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Store keeps connectors.
type Store interface {
	// Put creates or replaces a connector. Two connectors of one owner cannot
	// share a name: the error wraps ErrInvalid.
	Put(ctx context.Context, c Connector) error
	Get(ctx context.Context, id string) (Connector, error)
	List(ctx context.Context, owner string) ([]Connector, error)
	// Delete is not an error when the connector does not exist.
	Delete(ctx context.Context, id string) error
}

// ErrNameTaken is the error of a name another connector of the owner has. It
// wraps ErrInvalid.
var ErrNameTaken = fmt.Errorf("%w: another connector has this name", ErrInvalid)

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func (c Connector) clone() Connector {
	c.Args = append([]string(nil), c.Args...)
	c.Secrets = append([]byte(nil), c.Secrets...)
	return c
}

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,100}$")
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)

// A header the HTTP stack owns: setting it would break the protocol or let the
// connector pose as another host.
var reservedHeaders = map[string]bool{"host": true, "content-length": true, "transfer-encoding": true, "connection": true,
	"content-type": true, "accept": true, "upgrade": true, "te": true, "trailer": true, "mcp-session-id": true}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// ValidateSecrets checks the sealed part of a connector.
func ValidateSecrets(s Secrets) error {
	if len(s.Token) > MaxValueBytes || hasControl(s.Token) {
		return bad("the token is too long or has control characters")
	}
	if len(s.Headers) > MaxHeaders {
		return bad("at most %d headers", MaxHeaders)
	}
	for k, v := range s.Headers {
		switch {
		case !headerName.MatchString(k):
			return bad("%q is not a valid header name", k)
		case reservedHeaders[strings.ToLower(k)]:
			return bad("the header %s cannot be set", k)
		case len(v) > MaxValueBytes || hasControl(v):
			return bad("the value of the header %s is too long or has control characters", k)
		}
	}
	if len(s.Env) > MaxEnv {
		return bad("at most %d environment variables", MaxEnv)
	}
	for k, v := range s.Env {
		if !envName.MatchString(k) {
			return bad("%q is not a valid variable name", k)
		}
		if len(v) > MaxValueBytes || strings.ContainsRune(v, 0) {
			return bad("the value of %s is too long", k)
		}
	}
	return nil
}

// Validate checks the plain fields of a connector. An address of a private
// network is refused only for an enabled connector: a disabled one may be saved
// (an import or a conversion makes them) and is checked when it is switched on.
// The check at connection time is the one that counts; see CheckURL.
func Validate(c Connector, allowPrivate bool) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(c.Name)); n == 0 || n > MaxNameRunes {
		return bad("the name must have 1 to %d characters", MaxNameRunes)
	}
	switch c.Transport {
	case TransportHTTP:
		if c.Command != "" || len(c.Args) > 0 {
			return bad("an http connector has no command")
		}
		if err := CheckURL(c.URL, c.Enabled && !allowPrivate); err != nil {
			return err
		}
	case TransportStdio:
		if c.URL != "" {
			return bad("a stdio connector has no URL")
		}
		if strings.TrimSpace(c.Command) == "" || strings.ContainsRune(c.Command, 0) {
			return bad("a stdio connector needs a command")
		}
		if len(c.Args) > MaxArgs {
			return bad("at most %d arguments", MaxArgs)
		}
		for _, a := range c.Args {
			if len(a) > MaxValueBytes || strings.ContainsRune(a, 0) {
				return bad("an argument is too long")
			}
		}
	default:
		return bad("the transport must be %q or %q", TransportHTTP, TransportStdio)
	}
	return nil
}

// CheckURL checks the address of a remote connector: http or https, a host, no
// credentials in the address. With refusePrivate it also refuses a host that is
// plainly internal (an address of a private network, localhost). A name that
// resolves to an internal address passes here: the connection itself refuses it
// at dial time, which a DNS answer that changes cannot get around.
func CheckURL(raw string, refusePrivate bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return bad("the URL is not valid")
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return bad("the URL must start with http:// or https://")
	}
	host := u.Hostname()
	if host == "" {
		return bad("the URL has no host")
	}
	if u.User != nil {
		return bad("the URL must not hold a user name or password: use the token or a header")
	}
	if !refusePrivate {
		return nil
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return bad("%s", errPrivate)
	}
	if ip := net.ParseIP(host); ip != nil && !httpclient.IsPublicIP(ip) {
		return bad("%s", errPrivate)
	}
	return nil
}

const errPrivate = "the address is on a private or internal network. An administrator can allow such addresses in the settings (mcp_connectors_allow_private)"
