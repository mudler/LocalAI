// Package credentials matches outbound download URLs to operator-configured
// credentials. Every download path (plain HTTP, go-containerregistry, oras)
// reads the same store, so a new call site cannot silently stay anonymous.
package credentials

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"regexp"
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
	// #nosec G304 -- path comes from the operator's credentials-file setting or data directory, not a request.
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
		return nil, fmt.Errorf("parsing credentials: %w", redactDecodeError(err))
	}
	s := &Store{}
	for i, e := range entries {
		c, err := newCredential(e, lookupEnv)
		if err != nil {
			var bad *invalidMatchError
			if errors.As(err, &bad) {
				// A match that failed to parse may be a URL with a token in it,
				// so only a match that parsed is safe to quote.
				return nil, fmt.Errorf("credentials entry %d: %w", i+1, err)
			}
			return nil, fmt.Errorf("credentials entry %d (match %q): %w", i+1, e.Match, err)
		}
		c.warnIfUnresolved()
		s.creds = append(s.creds, c)
	}
	return s, nil
}

var (
	unknownFieldRe = regexp.MustCompile(`^line (\d+): field (\S+) not found in type `)
	errorLineRe    = regexp.MustCompile(`^line (\d+): `)
	quotedSpanRe   = regexp.MustCompile("`[^`]*`|'[^']*'|\"[^\"]*\"")
)

// redactDecodeError rebuilds a yaml.TypeError from line numbers only, because
// yaml.v3 quotes the offending scalar, which in this file is often a secret
// (for example a token written where a header mapping belongs). Unknown-key
// messages are kept since they name the key, not a value.
func redactDecodeError(err error) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		// Other decode failures quote scalars too (for example a secret under
		// a mismatched !!int tag), so every quoted span goes and map keys,
		// which yaml.v3 prints unquoted, are cut off. Line numbers and the
		// fixed parser wording stay because they are what locates the typo.
		msg := quotedSpanRe.ReplaceAllString(err.Error(), "<redacted>")
		if before, _, ok := strings.Cut(msg, "invalid map key:"); ok {
			msg = before + "invalid map key"
		}
		return errors.New(msg)
	}
	msgs := make([]string, 0, len(te.Errors))
	for _, line := range te.Errors {
		if m := unknownFieldRe.FindStringSubmatch(line); m != nil {
			msgs = append(msgs, fmt.Sprintf("line %s: unknown key %s", m[1], m[2]))
			continue
		}
		if m := errorLineRe.FindStringSubmatch(line); m != nil {
			msgs = append(msgs, fmt.Sprintf("line %s: value has the wrong type", m[1]))
			continue
		}
		msgs = append(msgs, "value has the wrong type")
	}
	return fmt.Errorf("credentials file has entries of the wrong type or unknown keys: %s", strings.Join(msgs, "; "))
}

// String, GoString and LogValue name rules by match only. They are defined on
// Store because fmt prints the unexported creds field without consulting
// Credential.String. String and GoString take a value so that both Store and
// *Store are covered.
func (s Store) String() string {
	return fmt.Sprintf("credentials.Store(%d rules: %s)", len(s.creds), strings.Join(s.matches(), ", "))
}

func (s Store) GoString() string {
	return s.String()
}

// LogValue takes a pointer so that logging Default() before a store is
// installed does not panic inside slog. A Store value logged without it falls
// back to String (text) or an empty object (JSON).
func (s *Store) LogValue() slog.Value {
	if s == nil {
		return slog.GroupValue(slog.Int("rules", 0))
	}
	return slog.GroupValue(slog.Int("rules", len(s.creds)), slog.Any("matches", s.matches()))
}

func (s Store) matches() []string {
	out := make([]string, len(s.creds))
	for i, c := range s.creds {
		out[i] = c.Match
	}
	return out
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
	r := secretRef{env: env, file: file}
	if literal != "" {
		r.literal = &literal
	}
	return r, nil
}

// invalidMatchError marks a match that did not parse. Its message never
// includes the match text, and Parse relies on the type to leave it unquoted.
type invalidMatchError struct {
	msg string
}

func (e *invalidMatchError) Error() string {
	return e.msg
}

func invalidMatch(msg string) error {
	return &invalidMatchError{msg: msg}
}

func parseMatch(m string) (scheme, host, path string, err error) {
	m = strings.TrimSpace(m)
	if m == "" {
		return "", "", "", invalidMatch("match is required")
	}
	// Match ignores query strings and fragments, so such a rule could never
	// apply, and a query string is where signed URLs carry their token.
	if strings.Contains(m, "?") {
		return "", "", "", invalidMatch("match must not contain a query string")
	}
	if strings.Contains(m, "#") {
		return "", "", "", invalidMatch("match must not contain a fragment")
	}
	before, rest, hasScheme := strings.Cut(m, "://")
	if !hasScheme {
		rest = m
	}
	host, path, _ = strings.Cut(rest, "/")
	// Checked before the scheme so that a user:token@host match is refused as
	// userinfo whatever its scheme: such a rule never matches a request, and
	// Match is printed everywhere a rule is named.
	if strings.Contains(host, "@") || (hasScheme && strings.Contains(before, "@")) {
		return "", "", "", invalidMatch("match must not contain credentials (userinfo)")
	}
	if hasScheme {
		scheme = strings.ToLower(before)
		if scheme != "http" && scheme != "https" {
			return "", "", "", invalidMatch("unsupported scheme in match: only http and https are allowed")
		}
	}
	if host == "" {
		return "", "", "", invalidMatch("match has no host")
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
	// Allowlist: an empty scheme ("//host/path") or ws/ftp must never pick up
	// credentials meant for HTTPS.
	if scheme != "https" && scheme != "http" {
		return Credential{}, false
	}
	// Prefix scoping is meaningless once a segment can climb out of it, and a
	// server may resolve "..", including percent-encoded slashes, after we match.
	if hasDotSegment(u.Path) || hasDotSegment(decodedEscapedPath(u)) {
		return Credential{}, false
	}
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

func decodedEscapedPath(u *url.URL) string {
	var out []string
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		dec, err := url.PathUnescape(seg)
		if err != nil {
			dec = seg
		}
		out = append(out, dec)
	}
	return strings.Join(out, "/")
}

func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

func pathMatches(rule, target string) bool {
	return rule == "" || target == rule || strings.HasPrefix(target, rule+"/")
}
