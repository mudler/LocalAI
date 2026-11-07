package localaitools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The built-in skill "localai" teaches the model the client tools. It lives in
// the studio package, which imports this one, so the test reads the file.
// It keeps the skill from drifting: every tool and parameter it names must
// exist in the schemas that tools/list serves.
var _ = Describe("the localai skill", func() {
	var text string
	var props map[string]map[string]bool // tool -> parameter names
	var actions map[string]bool

	BeforeEach(func() {
		raw, err := os.ReadFile(filepath.Join("..", "studio", "builtinskills", "tree", "localai", "SKILL.md"))
		Expect(err).NotTo(HaveOccurred())
		text = string(raw)

		ctx, sess, done := connectInMemory(&fakeClient{}, Options{})
		DeferCleanup(done)
		res, err := sess.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		props, actions = map[string]map[string]bool{}, map[string]bool{}
		for _, t := range res.Tools {
			if ToolGroupOf(t.Name) != GroupClient {
				continue
			}
			b, _ := json.Marshal(t.InputSchema)
			var schema struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			}
			Expect(json.Unmarshal(b, &schema)).To(Succeed())
			props[t.Name] = map[string]bool{}
			for name, p := range schema.Properties {
				props[t.Name][name] = true
				if name == "action" {
					for _, a := range p.Enum {
						actions[a] = true
					}
				}
			}
		}
		Expect(props).NotTo(BeEmpty())
	})

	It("names every client tool, and only client tools, in its tool list", func() {
		line := regexp.MustCompile("(?m)^- `([a-z_]+)`")
		var listed []string
		for _, m := range line.FindAllStringSubmatch(text, -1) {
			listed = append(listed, m[1])
		}
		var want []string
		for name := range props {
			want = append(want, name)
		}
		Expect(listed).To(ConsistOf(want))
	})

	It("gives each tool only parameters that its schema has", func() {
		line := regexp.MustCompile("(?m)^- `([a-z_]+)` \\(([^)]*)\\)")
		checked := 0
		for _, m := range line.FindAllStringSubmatch(text, -1) {
			tool, params := m[1], regexp.MustCompile("`([a-z_0-9]+)`").FindAllStringSubmatch(m[2], -1)
			Expect(props).To(HaveKey(tool))
			for _, p := range params {
				Expect(props[tool]).To(HaveKey(p[1]), "%s has no parameter %q", tool, p[1])
			}
			checked++
		}
		Expect(checked).To(BeNumerically(">=", 10))
	})

	It("uses only names that exist anywhere else in backticks", func() {
		known := map[string]bool{"ref": true, "load_skill": false}
		for tool, ps := range props {
			known[tool] = true
			for p := range ps {
				known[p] = true
			}
		}
		for a := range actions {
			known[a] = true
		}
		for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(text, -1) {
			Expect(known[m[1]]).To(BeTrue(), "the skill names %q, which is no client tool, parameter or action", m[1])
		}
	})

	It("describes the actions of the action tools", func() {
		for _, a := range []string{ActionVerify, ActionIdentify, ActionCreate, ActionList, ActionDelete} {
			Expect(text).To(ContainSubstring("`"+a+"`"), a)
		}
		Expect(slices.Collect(func(yield func(string) bool) {
			for a := range actions {
				if !yield(a) {
					return
				}
			}
		})).To(ConsistOf(ActionVerify, ActionIdentify, ActionCreate, ActionList, ActionDelete))
	})

	It("stays compact and in plain English", func() {
		Expect(len(text)).To(BeNumerically("<=", 4800), "about 1,200 tokens")
		Expect(strings.Count(text, "—")).To(BeZero(), "no em dashes")
	})
})
