// Package credentials matches outbound download URLs to operator-configured
// credentials. Every download path (plain HTTP, go-containerregistry, oras)
// reads the same store, so a new call site cannot silently stay anonymous.
package credentials

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type fileHeader struct {
	Name      string `yaml:"name"`
	Value     string `yaml:"value"`
	ValueEnv  string `yaml:"value_env"`
	ValueFile string `yaml:"value_file"`
}

type fileEntry struct {
	Match         string      `yaml:"match"`
	Username      string      `yaml:"username"`
	Password      string      `yaml:"password"`
	PasswordEnv   string      `yaml:"password_env"`
	PasswordFile  string      `yaml:"password_file"`
	Bearer        string      `yaml:"bearer"`
	BearerEnv     string      `yaml:"bearer_env"`
	BearerFile    string      `yaml:"bearer_file"`
	Header        *fileHeader `yaml:"header"`
	AllowInsecure bool        `yaml:"allow_insecure"`
}

// Store is an immutable, ordered list of credential rules.
type Store struct {
	creds []Credential
}

// Load reads and validates a credentials file.
func Load(path string, lookupEnv LookupEnvFunc) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, lookupEnv)
}

// Parse validates a credentials document. Unknown keys are errors: a
// misspelled password_env would otherwise load as a rule with no secret and
// fail later with a confusing 401.
func Parse(data []byte, lookupEnv LookupEnvFunc) (*Store, error) {
	var entries []fileEntry
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&entries); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	s := &Store{}
	for i, e := range entries {
		c, err := newCredential(e, lookupEnv)
		if err != nil {
			return nil, fmt.Errorf("credentials entry %d (match %q): %w", i+1, e.Match, err)
		}
		c.warnIfUnresolved()
		s.creds = append(s.creds, c)
	}
	return s, nil
}

// Len reports how many rules the store holds.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	return len(s.creds)
}

func newCredential(e fileEntry, lookupEnv LookupEnvFunc) (Credential, error) {
	scheme, host, path, err := parseMatch(e.Match)
	if err != nil {
		return Credential{}, err
	}
	c := Credential{
		Match:         e.Match,
		allowInsecure: e.AllowInsecure,
		scheme:        scheme,
		host:          host,
		path:          path,
		lookupEnv:     lookupEnv,
		username:      e.Username,
	}
	if c.password, err = oneForm("password", e.Password, e.PasswordEnv, e.PasswordFile); err != nil {
		return Credential{}, err
	}
	if c.bearer, err = oneForm("bearer", e.Bearer, e.BearerEnv, e.BearerFile); err != nil {
		return Credential{}, err
	}

	kinds := 0
	if e.Username != "" || c.password.set() {
		if e.Username == "" || !c.password.set() {
			return Credential{}, errors.New("basic auth needs both username and password")
		}
		kinds++
		c.Kind = KindBasic
	}
	if c.bearer.set() {
		kinds++
		c.Kind = KindBearer
	}
	if e.Header != nil {
		if e.Header.Name == "" {
			return Credential{}, errors.New("header.name is required")
		}
		if c.headerValue, err = oneForm("value", e.Header.Value, e.Header.ValueEnv, e.Header.ValueFile); err != nil {
			return Credential{}, err
		}
		if !c.headerValue.set() {
			return Credential{}, errors.New("header value is required")
		}
		c.headerName = e.Header.Name
		kinds++
		c.Kind = KindHeader
	}
	if kinds != 1 {
		return Credential{}, fmt.Errorf("exactly one of basic (username + password), bearer, or header is required, found %d", kinds)
	}
	return c, nil
}

func oneForm(field, literal, env, file string) (secretRef, error) {
	n := 0
	for _, v := range []string{literal, env, file} {
		if v != "" {
			n++
		}
	}
	if n > 1 {
		return secretRef{}, fmt.Errorf("set only one of %s, %s_env, %s_file", field, field, field)
	}
	return secretRef{literal: literal, env: env, file: file}, nil
}

func parseMatch(m string) (scheme, host, path string, err error) {
	m = strings.TrimSpace(m)
	if m == "" {
		return "", "", "", errors.New("match is required")
	}
	if before, after, ok := strings.Cut(m, "://"); ok {
		scheme = strings.ToLower(before)
		if scheme != "http" && scheme != "https" {
			return "", "", "", fmt.Errorf("unsupported scheme %q in match", scheme)
		}
		m = after
	}
	host, path, _ = strings.Cut(m, "/")
	if host == "" {
		return "", "", "", errors.New("match has no host")
	}
	return scheme, normalizeHost(scheme, host), strings.Trim(path, "/"), nil
}

// normalizeHost folds spellings of the same endpoint together so a rule
// written one way matches a request made another way.
func normalizeHost(scheme, host string) string {
	host = strings.ToLower(host)
	if scheme == "http" {
		host = strings.TrimSuffix(host, ":80")
	} else {
		host = strings.TrimSuffix(host, ":443")
	}
	switch host {
	case "docker.io", "registry-1.docker.io":
		return "index.docker.io"
	}
	return host
}

type matchTarget struct {
	host string
	path string
}

// Match returns the rule for rawURL: the longest matching path wins, and ties
// go to the rule that appears first in the file.
func (s *Store) Match(rawURL string) (Credential, bool) {
	if s.Len() == 0 {
		return Credential{}, false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return Credential{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	host := normalizeHost(scheme, u.Host)
	path := strings.Trim(u.Path, "/")

	targets := []matchTarget{{host: host, path: path}}
	// github: URIs are fetched from raw.githubusercontent.com, but operators
	// think of the repository as github.com/<org>/<repo>.
	if host == "raw.githubusercontent.com" {
		targets = append(targets, matchTarget{host: "github.com", path: path})
	}

	best, bestLen := -1, -1
	for i, c := range s.creds {
		if c.scheme != "" && c.scheme != scheme {
			continue
		}
		if scheme == "http" && !c.allowInsecure {
			continue
		}
		for _, t := range targets {
			if c.host != t.host || !pathMatches(c.path, t.path) {
				continue
			}
			if len(c.path) > bestLen {
				best, bestLen = i, len(c.path)
			}
		}
	}
	if best < 0 {
		return Credential{}, false
	}
	return s.creds[best], true
}

func pathMatches(rule, target string) bool {
	return rule == "" || target == rule || strings.HasPrefix(target, rule+"/")
}
