// SPDX-License-Identifier: MIT
package localai_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostPolicyArchitecture(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Host policy architecture")
}

var _ = Describe("generic host policy architecture", func() {
	It("keeps official addon identities out of generic CLI and config policy code", func() {
		// Tests may retain historical fixtures. Deployment policy is deliberately
		// outside these packages; unrelated model fields (e.g. quantization) are not IDs.
		ids := []string{"fine-tuning", "quantization", "sandbox", "office", "web", "telegram", "mail"}
		for _, root := range []string{"core/cli", "core/config"} {
			err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				if err != nil {
					return err
				}
				policyCode := strings.Contains(filepath.Base(path), "addon_host_") || path == "core/cli/run.go"
				ast.Inspect(file, func(node ast.Node) bool {
					literal, ok := node.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						return true
					}
					value, err := strconv.Unquote(literal.Value)
					Expect(err).NotTo(HaveOccurred())
					for _, id := range ids {
						Expect(value).NotTo(ContainSubstring(id+"="), "%s embeds an official grant", path)
						if policyCode {
							Expect(value).NotTo(Equal(id), "%s embeds an official addon identity", path)
						}
					}
					Expect(value).NotTo(ContainSubstring("default:\"fine-tuning="), "%s embeds a CLI default", path)
					return true
				})
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		}
	})
})

var _ = Describe("typed extension architecture", func() {
	It("keeps provider and Home dispatch independent of official addon IDs", func() {
		// Only generic live dispatch surfaces are guarded. Legacy URL redirects,
		// pin migration, native Studio code, deployment policy, and tests are not
		// live addon dispatch and deliberately do not belong to this list.
		paths := []string{"core/services/addons/apps", "core/services/addons/gateway/binding.go", "core/services/studio/executionselection.go"}
		for _, root := range paths {
			Expect(filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				if err != nil {
					return err
				}
				ast.Inspect(f, func(n ast.Node) bool {
					lit, ok := n.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return true
					}
					value, err := strconv.Unquote(lit.Value)
					Expect(err).NotTo(HaveOccurred())
					Expect(value).NotTo(BeElementOf("studio", "sandbox"), "%s hardcodes a product ID in generic dispatch", path)
					return true
				})
				return nil
			})).To(Succeed())
		}
		data, err := os.ReadFile("core/http/react-ui/src/addons/homeCommands.js")
		Expect(err).NotTo(HaveOccurred())
		for _, literal := range []string{"'studio'", `"studio"`, "'sandbox'", `"sandbox"`} {
			Expect(string(data)).NotTo(ContainSubstring(literal))
		}
	})
})

var _ = Describe("provider-neutral host code", func() {
	// officialIDs are the IDs of the add-ons the repository ships. Host code
	// outside deployment policy must not name them as literals.
	officialIDs := []string{
		"face", "fine-tuning", "gdrive", "github", "gmail", "memory", "office", "quantization", "sandbox",
		"slack", "studio", "tables", "talk", "telegram", "voice", "web",
	}
	// allowed maps "path: literal" to the reason that the literal is not an
	// add-on identity. Keep every entry narrow and justified.
	allowed := map[string]string{
		"core/http/auth/models.go: github":                    "the host's own sign-in identity provider, not an add-on",
		"core/http/auth/permissions.go: quantization":         "built-in permission name for the host's native feature, not an add-on dispatch",
		"core/http/auth/usage.go: web":                        "usage source label for a session-cookie request, not an add-on",
		"core/services/studio/egress/registries.json: github": "egress preset id for well-known release hosts, not an add-on dispatch",
	}

	scan := func(root string) []string {
		var hits []string
		add := func(path, value string) {
			for _, id := range officialIDs {
				if value == id {
					hits = append(hits, path+": "+id)
				}
			}
		}
		Expect(filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			switch {
			case strings.HasSuffix(path, ".go"):
				f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				if err != nil {
					return err
				}
				ast.Inspect(f, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						value, err := strconv.Unquote(lit.Value)
						Expect(err).NotTo(HaveOccurred())
						add(path, value)
					}
					return true
				})
			case strings.HasSuffix(path, ".json"):
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				// The presets file allows comments and trailing commas, so read
				// its quoted strings directly instead of parsing it.
				for _, m := range quotedString.FindAllStringSubmatch(string(data), -1) {
					add(path, m[1])
				}
			}
			return nil
		})).To(Succeed())
		return hits
	}

	It("keeps official add-on IDs out of auth, connectors and egress presets", func() {
		used := map[string]bool{}
		for _, root := range []string{"core/http/auth", "core/services/studio/connectors", "core/services/studio/egress/registries.json"} {
			for _, hit := range scan(root) {
				if _, ok := allowed[hit]; ok {
					used[hit] = true
					continue
				}
				Fail("official add-on ID literal in host code: " + hit)
			}
		}
		for entry := range allowed {
			Expect(used).To(HaveKey(entry), "stale allowlist entry %q", entry)
		}
	})
})

var quotedString = regexp.MustCompile(`"([^"\\]*)"`)

// The OAuth logins of add-ons must work for any service, so nothing in the
// host or the contract may name one: only the add-on's manifest does. The
// files of the OAuth surface are found by these patterns, so a new OAuth file
// next to the others is checked without a change here. Each pattern must
// match a file: a pattern that matches none fails the guard, so a move is
// noticed. Test files are skipped.
var oauthGoGlobs = []string{
	"pkg/addons/contract/*oauth*.go",
	"core/services/studio/*oauth*.go",
	"core/services/studio/connectorlogin*.go",
	"core/services/studio/connectorcred*.go",
	"core/services/studio/*/*oauth*.go",
	"core/services/studio/oauthtoken/*.go",
	"core/services/addons/gateway/*oauth*.go",
	"core/http/endpoints/studio/*oauth*.go",
	// The application wiring that reads the descriptor and answers calls.
	"core/application/studio_connectors*.go",
	"core/application/addon_gateway*.go",
}

var oauthUIGlobs = []string{
	"core/http/react-ui/src/*/*OAuth*.js",
	"core/http/react-ui/src/*/*/*OAuth*.jsx",
	"core/http/react-ui/src/components/connectors/*.jsx",
	"core/http/react-ui/src/pages/ChatConnectors*.jsx",
}

// oauthSurface lists the files the patterns match, without tests. A pattern
// that matches nothing is returned in missing.
func oauthSurface(globs []string) (files, missing []string) {
	seen := map[string]bool{}
	for _, g := range globs {
		matches, err := filepath.Glob(g)
		n := 0
		for _, m := range matches {
			if strings.HasSuffix(m, "_test.go") || strings.HasSuffix(m, ".test.mjs") {
				continue
			}
			n++
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
		if err != nil || n == 0 {
			missing = append(missing, g)
		}
	}
	return files, missing
}

// oauthLocaleFiles are the namespaces whose OAuth strings are checked, in
// every locale. Only the strings under a key that names OAuth (oauth,
// oauthClient) and the reconnect strings of the Tools menu are read: the rest
// of a namespace belongs to other features.
var oauthLocaleFiles = []string{"addons.json", "chats.json"}

// oauthDenied are names and host names of identity and cloud services, lower
// case. The list is test data: production code must not need it, and it is
// long on purpose. A name matches as a whole word, so "aws" does not match
// "draws"; a host name matches anywhere it is not part of a longer name.
var oauthDenied = []string{
	// identity providers and single sign-on
	"auth0", "okta", "onelogin", "jumpcloud", "keycloak", "fusionauth", "zitadel", "authentik", "ory hydra",
	"pingidentity", "ping identity", "duo security", "cognito", "entra", "azure ad", "azuread", "adfs",
	"clerk.com", "workos", "stytch", "descope", "frontegg", "supabase", "firebase", "gotrue",
	// large platforms
	"google", "gmail", "youtube", "microsoft", "azure", "outlook", "hotmail", "office365", "onedrive", "sharepoint",
	"apple", "icloud", "amazon", "aws", "facebook", "instagram", "whatsapp", "twitter", "linkedin",
	"yahoo", "yandex", "baidu", "alibaba", "aliyun", "tencent", "wechat", "naver", "kakao",
	// developer and work tools
	"github", "gitlab", "bitbucket", "atlassian", "jira", "confluence", "slack", "discord", "telegram",
	"notion", "dropbox", "box.com", "salesforce", "hubspot", "zendesk", "freshdesk", "servicenow", "workday",
	"asana", "trello", "figma", "miro", "clickup", "linear.app", "zoom", "webex", "spotify", "twitch", "reddit",
	"paypal", "stripe", "shopify", "square", "xero", "quickbooks", "intuit", "docusign", "adobe", "mailchimp",
	"twilio", "sendgrid", "zoho", "heroku", "vercel", "netlify", "cloudflare", "digitalocean", "oracle", "ibm",
	// host names
	"accounts.google.com", "oauth2.googleapis.com", "googleapis.com", "login.microsoftonline.com", "login.live.com",
	"graph.microsoft.com", "appleid.apple.com", "github.com", "api.github.com", "gitlab.com", "bitbucket.org",
	"slack.com", "discord.com", "facebook.com", "graph.facebook.com", "api.twitter.com", "x.com", "linkedin.com",
	"okta.com", "oktapreview.com", "auth0.com", "amazoncognito.com", "amazonaws.com", "dropboxapi.com",
	"login.salesforce.com", "atlassian.com", "auth.atlassian.com", "api.notion.com", "zoom.us", "yandex.ru",
	"login.yahoo.com", "spotify.com", "id.twitch.tv",
}

// oauthReviewedParams are the authorize parameters a review accepted. A name
// added to the registry of the contract must be added here in the same
// reviewed change, with a generic reason: none may serve one service only.
var oauthReviewedParams = []string{
	"prompt",                 // OpenID Connect: the consent or login prompt
	"audience",               // the API the token is for
	"resource",               // RFC 8707 resource indicator
	"access_type",            // asks for a refresh token (value offline only)
	"include_granted_scopes", // incremental consent (value true only)
}

// oauthDeniedImports are import path prefixes of OAuth libraries and service
// SDKs. The host has its own small token client; a library would keep the
// response body in its errors, and an SDK would tie the host to a service.
var oauthDeniedImports = []string{
	"golang.org/x/oauth2", "github.com/coreos/go-oidc", "github.com/zitadel/oidc", "github.com/markbates/goth",
	"github.com/dghubble/", "cloud.google.com/", "google.golang.org/api", "github.com/aws/", "github.com/Azure/",
	"github.com/AzureAD/", "github.com/microsoft/", "github.com/okta/", "github.com/auth0/", "github.com/google/go-github",
	"github.com/slack-go/", "github.com/bwmarrin/discordgo", "github.com/huandu/facebook", "github.com/dropbox/",
	"github.com/stripe/", "github.com/salesforce",
}

var oauthDeniedPattern = func() *regexp.Regexp {
	quoted := make([]string, len(oauthDenied))
	for i, d := range oauthDenied {
		quoted[i] = regexp.QuoteMeta(d)
	}
	return regexp.MustCompile(`(?:^|[^a-z0-9-])(` + strings.Join(quoted, "|") + `)(?:$|[^a-z0-9-])`)
}()

// oauthNamesService returns the denied names the text holds.
func oauthNamesService(text string) []string {
	var out []string
	for _, m := range oauthDeniedPattern.FindAllStringSubmatch(strings.ToLower(text), -1) {
		out = append(out, m[1])
	}
	return out
}

// oauthGoViolations checks one Go source: its string literals, its imports
// and, in the contract, the registry of authorize parameters.
func oauthGoViolations(path string, src []byte) []string {
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return []string{path + ": does not parse: " + err.Error()}
	}
	var out []string
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		for _, denied := range oauthDeniedImports {
			if strings.HasPrefix(p, denied) {
				out = append(out, path+": imports "+p)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if _, ok := n.(*ast.ImportSpec); ok {
			// Import paths are checked above; the module path names a code host.
			return false
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				value = lit.Value
			}
			for _, name := range oauthNamesService(value) {
				out = append(out, path+": a string literal names "+name)
			}
		}
		return true
	})
	if names, found := oauthParamRegistry(f); found {
		for _, name := range names {
			if !slices.Contains(oauthReviewedParams, name) {
				out = append(out, path+": the authorize parameter "+name+" is not on the reviewed list")
			}
		}
	}
	return out
}

// oauthParamRegistry returns the names of the variable OAuthAuthorizeParams,
// and whether the file declares it.
func oauthParamRegistry(f *ast.File) ([]string, bool) {
	var names []string
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "OAuthAuthorizeParams" || len(spec.Values) != 1 {
			return true
		}
		found = true
		list, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			names = append(names, "<not a literal list>")
			return false
		}
		for _, elt := range list.Elts {
			entry, ok := elt.(*ast.CompositeLit)
			if !ok {
				names = append(names, "<not a literal entry>")
				continue
			}
			name := "<no literal name>"
			for _, field := range entry.Elts {
				kv, ok := field.(*ast.KeyValueExpr)
				if key, isIdent := kv.Key.(*ast.Ident); !ok || !isIdent || key.Name != "Name" {
					continue
				}
				if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					name, _ = strconv.Unquote(lit.Value)
				}
			}
			names = append(names, name)
		}
		return false
	})
	return names, found
}

// oauthUIViolations checks one React source as text, comments included: JSX
// text is not a string literal, and a service named in a comment is a hint
// that the code serves it.
func oauthUIViolations(path string, src []byte) []string {
	var out []string
	for _, name := range oauthNamesService(string(src)) {
		out = append(out, path+": names "+name)
	}
	return out
}

// oauthLocaleStrings returns the OAuth strings of one locale namespace by
// key path.
func oauthLocaleStrings(src []byte) (map[string]string, error) {
	var doc map[string]any
	if err := json.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	out := map[string]string{}
	var walk func(key string, v any, inOAuth bool)
	walk = func(key string, v any, inOAuth bool) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				walk(key+"."+k, child, inOAuth || strings.Contains(strings.ToLower(k), "oauth"))
			}
		case string:
			last := key[strings.LastIndex(key, ".")+1:]
			if inOAuth || strings.HasPrefix(last, "reconnect") {
				out[key] = t
			}
		}
	}
	walk("", doc, false)
	return out, nil
}

// oauthLocaleViolations checks the OAuth strings of one locale namespace.
func oauthLocaleViolations(path string, src []byte) []string {
	texts, err := oauthLocaleStrings(src)
	if err != nil {
		return []string{path + ": does not parse: " + err.Error()}
	}
	var out []string
	for key, text := range texts {
		for _, name := range oauthNamesService(text) {
			out = append(out, path+": "+key+" names "+name)
		}
	}
	return out
}

// oauthSurfaceViolations reads every file of the OAuth surface.
func oauthSurfaceViolations() []string {
	var out []string
	read := func(path string, check func(string, []byte) []string) {
		src, err := os.ReadFile(path) // #nosec G304 -- the guard reads files of this repository
		if err != nil {
			out = append(out, path+": "+err.Error())
			return
		}
		out = append(out, check(path, src)...)
	}
	goFiles, missing := oauthSurface(oauthGoGlobs)
	uiFiles, missingUI := oauthSurface(oauthUIGlobs)
	for _, g := range append(missing, missingUI...) {
		out = append(out, g+": matches no file of the OAuth surface")
	}
	for _, path := range goFiles {
		read(path, oauthGoViolations)
	}
	for _, path := range uiFiles {
		read(path, oauthUIViolations)
	}
	locales, err := os.ReadDir("core/http/react-ui/public/locales")
	if err != nil || len(locales) == 0 {
		return append(out, "no locales to check")
	}
	for _, l := range locales {
		for _, ns := range oauthLocaleFiles {
			read(filepath.Join("core/http/react-ui/public/locales", l.Name(), ns), oauthLocaleViolations)
		}
	}
	return out
}

var _ = Describe("OAuth logins that name no service", func() {
	It("keeps service names and hosts, unreviewed authorize parameters and OAuth libraries out of the host", func() {
		Expect(oauthSurfaceViolations()).To(BeEmpty())
		src, err := os.ReadFile("pkg/addons/contract/oauth.go")
		Expect(err).NotTo(HaveOccurred())
		f, err := parser.ParseFile(token.NewFileSet(), "oauth.go", src, 0)
		Expect(err).NotTo(HaveOccurred())
		names, found := oauthParamRegistry(f)
		Expect(found).To(BeTrue(), "the guard reads the registry of authorize parameters")
		Expect(names).To(ConsistOf(oauthReviewedParams), "the registry and the reviewed list change together")
		for _, ns := range oauthLocaleFiles {
			src, err := os.ReadFile(filepath.Join("core/http/react-ui/public/locales/en", ns))
			Expect(err).NotTo(HaveOccurred())
			texts, err := oauthLocaleStrings(src)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(texts)).To(BeNumerically(">=", 10), "the guard finds the OAuth strings of %s", ns)
		}
	})

	It("finds the OAuth files by pattern, the application wiring and the Connectors page included, and fails on a pattern that matches nothing", func() {
		goFiles, missing := oauthSurface(oauthGoGlobs)
		Expect(missing).To(BeEmpty())
		Expect(goFiles).To(ContainElements("core/application/studio_connectors.go", "core/application/addon_gateway.go",
			"core/services/studio/connectorlogin.go", "core/services/studio/connectors/gorm_oauth.go", "pkg/addons/contract/oauth.go"))
		Expect(goFiles).NotTo(ContainElement(HaveSuffix("_test.go")))
		uiFiles, missing := oauthSurface(oauthUIGlobs)
		Expect(missing).To(BeEmpty())
		Expect(uiFiles).To(ContainElements("core/http/react-ui/src/pages/ChatConnectors.jsx", "core/http/react-ui/src/utils/connectorOAuth.js",
			"core/http/react-ui/src/components/connectors/OAuthConnection.jsx", "core/http/react-ui/src/components/hostcontrols/OAuthClientSettings.jsx"))
		_, missing = oauthSurface([]string{"core/services/studio/moved_oauth_*.go"})
		Expect(missing).To(ConsistOf("core/services/studio/moved_oauth_*.go"))
	})

	It("fails on a service named in code, a locale or the UI, an unreviewed parameter and an OAuth library", func() {
		goSrc := []byte(`package contract

import "golang.org/x/oauth2"

var OAuthAuthorizeParams = []OAuthParamSpec{{Name: "prompt"}, {Name: "hd"}}

const authorize = "https://accounts.google.com/o/oauth2/auth"

var _ = oauth2.Config{}
`)
		Expect(oauthGoViolations("synthetic.go", goSrc)).To(ConsistOf(
			"synthetic.go: imports golang.org/x/oauth2",
			"synthetic.go: a string literal names accounts.google.com",
			"synthetic.go: the authorize parameter hd is not on the reviewed list",
		))
		Expect(oauthGoViolations("generic.go", []byte("package x\n\n// Not GitHub: comments may explain.\nconst host = \"login.provider-a.example.test\"\nconst s = \"draws on the store\"\n"))).To(BeEmpty())
		Expect(oauthUIViolations("synthetic.jsx", []byte(`export const Hint = () => <p>Sign in with GitHub</p>`))).To(ConsistOf("synthetic.jsx: names github"))
		Expect(oauthLocaleViolations("synthetic.json", []byte(`{"tools":{"reconnect":"Reconnect to Okta"},"connectors":{"oauth":{"x":"Connected as {{account}}"},"title":"Telegram"}}`))).
			To(ConsistOf("synthetic.json: .tools.reconnect names okta"))
	})
})
