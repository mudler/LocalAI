// SPDX-License-Identifier: MIT
package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// CredentialKindOAuth2 is a credential slot whose secret the host obtains with
// the OAuth 2.0 authorization code flow and refreshes itself. The add-on gets
// a short-lived access token in Request.Credential and never sees the refresh
// token, the client secret or the authorization code. The slot then carries an
// OAuthDescriptor.
const CredentialKindOAuth2 = "oauth2"

// The PKCE method and the client authentications a descriptor can name.
const (
	OAuthPKCES256        = "S256"
	OAuthClientAuthBasic = "basic"
	OAuthClientAuthPost  = "post"
	// OAuthClientAuthNone is a public client that relies on PKCE alone.
	OAuthClientAuthNone = "none"
)

// Bounds of an OAuth descriptor.
const (
	MaxOAuthHosts        = 4
	MaxOAuthScopes       = 16
	MaxOAuthScopeBytes   = 128
	MaxOAuthParams       = 4
	MaxOAuthParamValue   = 128
	maxOAuthURLBytes     = 2048
	maxOAuthHostBytes    = 253
	maxOAuthLabelBytes   = 63
	oauthDigestDomainTag = "localai.addon.oauth2.descriptor.v1\n"
)

// OAuthDescriptor says how the host logs a person in to the service behind an
// add-on. It is data: it adds no route and makes no network call. Yet it
// chooses where the host sends the person and the tokens, so the host acts on
// it only after an administrator approved the exact descriptor, identified by
// Digest. A change of any field is a new descriptor and needs a new approval.
type OAuthDescriptor struct {
	// AuthorizeURL is where the person is sent to grant access.
	AuthorizeURL string `json:"authorizeUrl"`
	// TokenURL is where the host exchanges a code and refreshes a token.
	TokenURL string `json:"tokenUrl"`
	// RevokeURL, when set, is where the host revokes a token on disconnect.
	RevokeURL string `json:"revokeUrl,omitempty"`
	// Hosts are the only host names the three URLs may use: one to four
	// patterns, each an exact lower-case DNS name or "*." followed by a name of
	// two or more labels. A wildcard matches exactly one label.
	Hosts  []string    `json:"hosts"`
	Scopes OAuthScopes `json:"scopes"`
	// PKCE must be "S256".
	PKCE string `json:"pkce"`
	// AuthorizeParams are extra parameters of the authorize request, from the
	// closed registry OAuthAuthorizeParams.
	AuthorizeParams []OAuthParam `json:"authorizeParams,omitempty"`
	// ClientAuth is how the host authenticates at the token endpoint: "basic",
	// "post" or "none".
	ClientAuth string `json:"clientAuth"`
}

// OAuthScopes are the scopes the host asks for. Required scopes are always
// requested; optional ones only when the person ticks them.
type OAuthScopes struct {
	Required []string `json:"required,omitempty"`
	Optional []string `json:"optional,omitempty"`
}

// OAuthParam is one extra authorize parameter.
type OAuthParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// OAuthParamSpec is one entry of the closed registry of authorize parameters.
type OAuthParamSpec struct {
	Name string
	// Values, when set, lists the only values the parameter takes. Otherwise
	// the value must match oauthParamValue.
	Values []string
}

// OAuthAuthorizeParams is the closed registry of authorize parameters, like
// HostControls. Adding a name is a reviewed contract change. These are generic
// OAuth and OpenID Connect parameter names; none belongs to one service.
var OAuthAuthorizeParams = []OAuthParamSpec{
	{Name: "prompt"},
	{Name: "audience"},
	{Name: "resource"},
	{Name: "access_type", Values: []string{"offline"}},
	{Name: "include_granted_scopes", Values: []string{"true"}},
}

var (
	oauthParamValue = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,128}$`)
	oauthLabel      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// oauthProtocolParam reports a parameter the host sets itself. A descriptor
// can never set one, whatever the registry holds.
func oauthProtocolParam(name string) bool {
	switch name {
	case "client_id", "client_secret", "redirect_uri", "response_type", "scope", "state", "code", "grant_type":
		return true
	}
	return strings.HasPrefix(name, "code_challenge")
}

// Validate checks the descriptor. It does no network call.
func (d OAuthDescriptor) Validate() error {
	if len(d.Hosts) == 0 || len(d.Hosts) > MaxOAuthHosts {
		return fmt.Errorf("oauth: between 1 and %d hosts", MaxOAuthHosts)
	}
	for i, h := range d.Hosts {
		if !validOAuthHostPattern(h) || slices.Contains(d.Hosts[:i], h) {
			return fmt.Errorf("oauth: invalid or repeated host pattern")
		}
	}
	for _, u := range []struct{ name, value string }{{"authorizeUrl", d.AuthorizeURL}, {"tokenUrl", d.TokenURL}} {
		if err := d.validURL(u.name, u.value); err != nil {
			return err
		}
	}
	if d.RevokeURL != "" {
		if err := d.validURL("revokeUrl", d.RevokeURL); err != nil {
			return err
		}
	}
	if err := d.Scopes.validate(); err != nil {
		return err
	}
	if d.PKCE != OAuthPKCES256 {
		return fmt.Errorf("oauth: pkce must be %q", OAuthPKCES256)
	}
	switch d.ClientAuth {
	case OAuthClientAuthBasic, OAuthClientAuthPost, OAuthClientAuthNone:
	default:
		return fmt.Errorf("oauth: clientAuth must be basic, post or none")
	}
	return validateOAuthParams(d.AuthorizeParams)
}

func (d OAuthDescriptor) validURL(field, s string) error {
	bad := func() error { return fmt.Errorf("oauth: invalid %s", field) }
	if s == "" || len(s) > maxOAuthURLBytes || strings.ContainsAny(s, "?#\\ ") {
		return bad()
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7e {
			return bad()
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return bad()
	}
	if p := u.Port(); p != "" && p != "443" {
		return bad()
	}
	host := u.Hostname()
	if host == "" || host != strings.ToLower(host) || !validDNSName(host, 1) {
		return bad()
	}
	if !slices.ContainsFunc(d.Hosts, func(p string) bool { return oauthHostMatches(p, host) }) {
		return fmt.Errorf("oauth: %s host is not in hosts", field)
	}
	return nil
}

// validDNSName reports a lower-case DNS name of at least minLabels labels that
// is not an IP literal and whose last label is not numeric.
func validDNSName(h string, minLabels int) bool {
	if h == "" || len(h) > maxOAuthHostBytes {
		return false
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return false
	}
	labels := strings.Split(h, ".")
	if len(labels) < minLabels {
		return false
	}
	for _, l := range labels {
		if len(l) > maxOAuthLabelBytes || !oauthLabel.MatchString(l) {
			return false
		}
	}
	last := labels[len(labels)-1]
	return strings.Trim(last, "0123456789") != ""
}

func validOAuthHostPattern(p string) bool {
	if rest, ok := strings.CutPrefix(p, "*."); ok {
		return validDNSName(rest, 2)
	}
	return validDNSName(p, 1)
}

func oauthHostMatches(pattern, host string) bool {
	suffix, wild := strings.CutPrefix(pattern, "*")
	if !wild {
		return pattern == host
	}
	label, ok := strings.CutSuffix(host, suffix)
	return ok && label != "" && !strings.Contains(label, ".")
}

func (s OAuthScopes) validate() error {
	if len(s.Required) > MaxOAuthScopes || len(s.Optional) > MaxOAuthScopes {
		return fmt.Errorf("oauth: at most %d required and %d optional scopes", MaxOAuthScopes, MaxOAuthScopes)
	}
	seen := map[string]bool{}
	for _, list := range [][]string{s.Required, s.Optional} {
		for _, sc := range list {
			if !validScopeToken(sc) || seen[sc] {
				return fmt.Errorf("oauth: invalid, repeated or overlapping scope")
			}
			seen[sc] = true
		}
	}
	return nil
}

// validScopeToken is a scope-token of RFC 6749 section 3.3: printable ASCII
// without space, double quote or backslash.
func validScopeToken(s string) bool {
	if s == "" || len(s) > MaxOAuthScopeBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

func validateOAuthParams(params []OAuthParam) error {
	if len(params) > MaxOAuthParams {
		return fmt.Errorf("oauth: at most %d authorize parameters", MaxOAuthParams)
	}
	seen := map[string]bool{}
	for _, p := range params {
		i := slices.IndexFunc(OAuthAuthorizeParams, func(s OAuthParamSpec) bool { return s.Name == p.Name })
		if i < 0 || oauthProtocolParam(p.Name) || seen[p.Name] {
			return fmt.Errorf("oauth: unknown, protocol or repeated authorize parameter")
		}
		seen[p.Name] = true
		spec := OAuthAuthorizeParams[i]
		if len(spec.Values) > 0 && !slices.Contains(spec.Values, p.Value) {
			return fmt.Errorf("oauth: invalid value for authorize parameter %s", p.Name)
		}
		if !oauthParamValue.MatchString(p.Value) {
			return fmt.Errorf("oauth: invalid value for authorize parameter %s", p.Name)
		}
	}
	return nil
}

// Digest is the SHA-256 of the canonical form of the descriptor, in lower-case
// hex. The canonical form does not depend on the order of fields or of the
// lists that are sets (hosts, scopes, parameters) or on the whitespace of the
// manifest, and it keeps required and optional scopes apart. The host binds an
// administrator's approval to this value.
func (d OAuthDescriptor) Digest() string {
	sorted := func(in []string) []string {
		out := slices.Clone(in)
		if out == nil {
			out = []string{}
		}
		slices.Sort(out)
		return out
	}
	params := slices.Clone(d.AuthorizeParams)
	if params == nil {
		params = []OAuthParam{}
	}
	slices.SortFunc(params, func(a, b OAuthParam) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Value, b.Value)
	})
	canonical := struct {
		AuthorizeURL    string       `json:"authorizeUrl"`
		TokenURL        string       `json:"tokenUrl"`
		RevokeURL       string       `json:"revokeUrl"`
		Hosts           []string     `json:"hosts"`
		Required        []string     `json:"required"`
		Optional        []string     `json:"optional"`
		PKCE            string       `json:"pkce"`
		AuthorizeParams []OAuthParam `json:"authorizeParams"`
		ClientAuth      string       `json:"clientAuth"`
	}{d.AuthorizeURL, d.TokenURL, d.RevokeURL, sorted(d.Hosts), sorted(d.Scopes.Required), sorted(d.Scopes.Optional), d.PKCE, params, d.ClientAuth}
	b, _ := json.Marshal(canonical)
	sum := sha256.Sum256(append([]byte(oauthDigestDomainTag), b...))
	return hex.EncodeToString(sum[:])
}
