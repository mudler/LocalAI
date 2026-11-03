package providerguard_test

// The test keeps core free of the names of shipped add-ons that provide a
// generic role: the text that a person or a model reads must come from the
// provider's declared title, or be generic.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/providerguard"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// matcher finds a name as a whole word, case sensitive: "Sandbox" is the
// add-on, "sandbox" the ordinary noun for an isolated environment.
func matcher(names []string) *regexp.Regexp {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	return regexp.MustCompile(`(^|[^\w-])(` + strings.Join(quoted, "|") + `)($|[^\w-])`)
}

// allowed lists the string literals that may name a provider, by the file
// they are in and the text, with the reason. Each one must still be found.
var allowed = []struct{ file, text, why string }{
	{"core/http/endpoints/localai/api_instructions.go", "Telegram connectors", "the one-time conversion of the legacy Telegram connectors"},
	{"core/schema/chats.go", `example:"Sandbox"`, "a sample value in the API documentation"},
	{"core/services/addons/tools/consumers.go", "such as Telegram", "an example of a connector platform in a note for add-on authors"},
	{"core/services/routing/piipattern/builtins.go", "JSON Web Token", "the name of a token format"},
	{"core/services/studio/webtools.go", "Web search", "the label of the built-in web search tool"},
	{"core/services/studio/webtools.go", "Web:", "the built-in Web tool group, the web as a noun"},
	{"core/services/studio/connectorsvc.go", "Telegram", "one-time migration of the legacy Telegram connector settings"},
	{"core/services/studio/convert/run.go", "Telegram", "one-time migration of the legacy Telegram bindings"},
	{"core/cli/run.go", "Telegram, Slack", "help text that gives the connector platforms as examples"},
	{"core/http/auth/features.go", "Telegram bots", "the name of a feature permission; the platform is the content"},
	{"core/services/routing/piipattern/builtins.go", "Slack token", "a secret pattern: Slack is the issuer of the token, not a provider"},
}

var _ = Describe("the strings of core", func() {
	It("reads the providers from the manifests of addons/", func() {
		names, err := providerguard.Names()
		Expect(err).NotTo(HaveOccurred())
		Expect(len(names)).To(BeNumerically(">=", 4), "expected the provider add-ons of addons/")
	})

	It("matches a name as a whole word and never the ordinary noun", func() {
		re := matcher([]string{"Sandbox", "Simple Office"})
		Expect(re.MatchString("Install the Sandbox to isolate")).To(BeTrue())
		Expect(re.MatchString("Open Simple Office")).To(BeTrue())
		Expect(re.MatchString("curl works in the sandbox")).To(BeFalse())
		Expect(re.MatchString("a Sandboxed frame")).To(BeFalse())
	})

	It("names no provider add-on in a user-facing or model-facing string outside the allow-list", func() {
		root, err := providerguard.RepoRoot()
		Expect(err).NotTo(HaveOccurred())
		names, err := providerguard.Names()
		Expect(err).NotTo(HaveOccurred())
		re := matcher(names)
		used := map[int]bool{}
		var found []string
		for _, dir := range []string{"core", "pkg"} {
			err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					// The sandbox backend is the add-on's own code that lives
					// in core for now; the other trees are not ours.
					switch d.Name() {
					case "node_modules", "vendor", "testdata", "sandbox", "react-ui":
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				fset := token.NewFileSet()
				f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
				if perr != nil {
					return nil
				}
				rel, _ := filepath.Rel(root, path)
				rel = filepath.ToSlash(rel)
				ast.Inspect(f, func(n ast.Node) bool {
					lit, ok := n.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return true
					}
					s, uerr := strconv.Unquote(lit.Value)
					if uerr != nil {
						s = lit.Value
					}
					hit := re.FindStringSubmatch(s)
					if hit == nil {
						return true
					}
					for i, a := range allowed {
						if a.file == rel && strings.Contains(s, a.text) {
							used[i] = true
							return true
						}
					}
					found = append(found, rel+":"+strconv.Itoa(fset.Position(lit.Pos()).Line)+": "+hit[2]+": "+strconv.Quote(s))
					return true
				})
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(found).To(BeEmpty(), "build the text from the provider's declared title or word it for any provider")
		for i, a := range allowed {
			Expect(used[i]).To(BeTrue(), "allow-list entry %s %q (%s) no longer matches; remove it", a.file, a.text, a.why)
		}
	})
})
