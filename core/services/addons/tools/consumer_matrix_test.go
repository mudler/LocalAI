// SPDX-License-Identifier: MIT
package tools_test

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// markers are the source patterns of code that discovers or executes a tool
// of an MCP session or of an agent library. A file that holds one is a
// consumer and needs a decision in tools.Consumers.
var markers = regexp.MustCompile(`ExecuteTool\(|ExecuteMCPToolCall|DiscoverMCPTools|NewToolExecutor\(|NamedSessionsFromMCPConfig|SessionsFromMCPConfig|\.CallTool\(|cogito\.WithMCPs|AssistantExecutor|LocalAIAssistantHolder|\.Executor\(\)|mcp\.NewClient\(|gomcp\.NewClient\(|\.ListTools\(`)

// withMCPsCall and toolFilterUse find the hand-over of live MCP sessions to the
// agent library and the gate that keeps the add-on namespace out of it.
var (
	withMCPsCall  = regexp.MustCompile(`cogito\.WithMCPs\(`)
	toolFilterUse = regexp.MustCompile(`cogito\.WithMCPToolFilter\([^)]*(SessionToolFilter\(\)|mcpToolFilter\(\)|IsToolName)`)
)

// sessionsWithoutFilter reports a source text that hands MCP sessions to the
// agent library without installing the namespace filter next to them. The
// library discovers and executes tools straight from the sessions, so the
// filter is the only guard on that path.
func sessionsWithoutFilter(text string) bool {
	return withMCPsCall.MatchString(text) && !toolFilterUse.MatchString(text)
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

// sourceFiles lists the non-test Go files under the product roots.
func sourceFiles(root string, match func(path, text string) bool) []string {
	GinkgoHelper()
	var out []string
	for _, dir := range []string{"core", "pkg", "cmd"} {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		Expect(filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "dist" || d.Name() == ".cache") {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			if match(filepath.ToSlash(rel), string(b)) {
				out = append(out, filepath.ToSlash(rel))
			}
			return nil
		})).To(Succeed())
	}
	sort.Strings(out)
	return out
}

var _ = Describe("Tool consumer matrix", func() {
	root := repoRoot()

	It("classifies every source file that discovers or executes tools", func() {
		// The matrix and the adapter name the markers in prose; they are not
		// consumers themselves.
		found := sourceFiles(root, func(path, text string) bool {
			return !strings.HasPrefix(path, "core/services/addons/tools/") && markers.MatchString(text)
		})
		classified := map[string]string{}
		for _, c := range tools.Consumers {
			Expect(c.Decision).To(BeElementOf(tools.Route, tools.Reject), c.ID)
			for _, f := range c.Files {
				Expect(classified).NotTo(HaveKey(f), "%s is classified twice", f)
				classified[f] = c.ID
			}
		}
		var unclassified []string
		for _, f := range found {
			if _, ok := classified[f]; !ok {
				unclassified = append(unclassified, f)
			}
		}
		Expect(unclassified).To(BeEmpty(), "add each file to tools.Consumers with a decision: it discovers or executes tools")
		// An entry must still point at code that holds a marker, so the matrix
		// cannot keep a decision for a file that no longer is a consumer.
		for f, id := range classified {
			Expect(found).To(ContainElement(f), "%s (%s) holds no tool marker any more", f, id)
		}
	})

	It("flags source that hands sessions to the agent library without the namespace filter", func() {
		Expect(sessionsWithoutFilter("opts = append(opts, cogito.WithMCPs(sessions...))")).To(BeTrue())
		Expect(sessionsWithoutFilter("cogito.WithMCPs(s...), cogito.WithMCPToolFilter(func(*mcp.ClientSession, string) bool { return true })")).To(BeTrue())
		Expect(sessionsWithoutFilter("cogito.WithMCPs(s...), cogito.WithMCPToolFilter(mcpTools.SessionToolFilter())")).To(BeFalse())
		Expect(sessionsWithoutFilter("cogito.WithMCPs(s...), cogito.WithMCPToolFilter(filter.mcpToolFilter())")).To(BeFalse())
		Expect(sessionsWithoutFilter("no sessions here")).To(BeFalse())
	})

	It("installs the namespace filter wherever sessions reach the agent library", func() {
		unfiltered := sourceFiles(root, func(path, text string) bool {
			return !strings.HasPrefix(path, "core/services/addons/tools/") && sessionsWithoutFilter(text)
		})
		Expect(unfiltered).To(BeEmpty(), "cogito.WithMCPs without cogito.WithMCPToolFilter lets add-on tools run unbrokered")
	})

	It("names a reason and an enforcement for every rejection and an actor for every route, with real files", func() {
		seen := map[string]bool{}
		for _, c := range tools.Consumers {
			Expect(seen).NotTo(HaveKey(c.ID))
			seen[c.ID] = true
			Expect(c.Surface).NotTo(BeEmpty(), c.ID)
			if c.Decision == tools.Reject {
				Expect(c.Reason).NotTo(BeEmpty(), c.ID)
				Expect(c.Enforcement).NotTo(BeEmpty(), c.ID)
			} else {
				Expect(c.Actor).NotTo(BeEmpty(), c.ID)
			}
			Expect(len(c.Files)+len(c.Evidence)).To(BeNumerically(">", 0), c.ID)
			for _, f := range append(append([]string{}, c.Files...), c.Evidence...) {
				_, err := os.Stat(filepath.Join(root, f))
				Expect(err).NotTo(HaveOccurred(), c.ID+": "+f)
			}
		}
	})

	It("keeps the table of the Tables documentation in step with the matrix", func() {
		f, err := os.Open(filepath.Join(root, "docs/content/features/tables.md"))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = f.Close() }()
		row := regexp.MustCompile("^\\| `([a-z0-9-]+)` \\| (route|reject) \\|")
		docs := map[string]string{}
		s := bufio.NewScanner(f)
		for s.Scan() {
			if m := row.FindStringSubmatch(s.Text()); m != nil {
				docs[m[1]] = m[2]
			}
		}
		want := map[string]string{}
		for _, c := range tools.Consumers {
			want[c.ID] = string(c.Decision)
		}
		Expect(docs).To(Equal(want))
	})

	It("lets only the brokered consumers import the adapter", func() {
		importers := sourceFiles(root, func(path, text string) bool {
			return strings.Contains(text, `"github.com/mudler/LocalAI/core/services/addons/tools"`) && !strings.HasPrefix(path, "core/services/addons/tools/")
		})
		Expect(importers).To(ConsistOf(
			"core/application/addon_gateway.go",      // builds the provider from the owner gateway
			"core/http/endpoints/mcp/addon_tools.go", // the one executor of the namespace
			"core/http/endpoints/mcp/tools.go",       // refuses the namespace in MCP sessions
			"core/services/agents/toolpolicy.go",     // drops the namespace from agent runs
		))
	})

	It("has no add-on tool and no add-on route in the admin assistant catalog", func() {
		files := sourceFiles(root, func(path, _ string) bool { return strings.HasPrefix(path, "pkg/mcp/localaitools/") })
		Expect(files).NotTo(BeEmpty())
		for _, path := range files {
			b, err := os.ReadFile(filepath.Join(root, path))
			Expect(err).NotTo(HaveOccurred())
			text := strings.ToLower(string(b))
			Expect(text).NotTo(ContainSubstring("/api/addons"), path)
			Expect(text).NotTo(ContainSubstring("addon__"), path)
		}
		// Tool constants hold no add-on lifecycle or approval name.
		b, err := os.ReadFile(filepath.Join(root, "pkg/mcp/localaitools/tools.go"))
		Expect(err).NotTo(HaveOccurred())
		constant := regexp.MustCompile(`Tool\w+\s*=\s*"([a-z_]+)"`)
		for _, m := range constant.FindAllStringSubmatch(string(b), -1) {
			Expect(m[1]).NotTo(MatchRegexp(`addon|proposal|confirm|approve`), m[1])
		}
	})
})
