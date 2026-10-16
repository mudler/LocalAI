// SPDX-License-Identifier: MIT
package tablesacceptance_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

// TestEvidenceManifest checks the evidence manifest the acceptance run wrote.
// scripts/addons/tables_acceptance.sh runs it after the suite with
// TABLES_ACCEPTANCE_CHECK_EVIDENCE=1; a plain `go test` of the package skips
// it, because the manifest does not exist before the suite ran.
//
// The manifest is evidence, so the check is strict: the platform is the native
// one, the commands are listed, both package digests are well formed and
// distinct, the case inventory is exactly the inventory of suites.json, every
// case passed, and a case that did not has a retained trace that exists.
func TestEvidenceManifest(t *testing.T) {
	if os.Getenv("TABLES_ACCEPTANCE_CHECK_EVIDENCE") != "1" {
		// Ginkgo's Skip works only inside a spec; this is a plain Go test.
		t.Skip("runs after the suite, from scripts/addons/tables_acceptance.sh") //nolint:forbidigo
	}
	g := NewWithT(t)
	// Every broken rule is reported, not only the first one.
	var problems []string
	problem := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	defer func() { g.Expect(problems).To(BeEmpty()) }()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	path, err := evidenceLocation(os.Getenv("TABLES_ACCEPTANCE_EVIDENCE"), root)
	g.Expect(err).NotTo(HaveOccurred())
	raw, err := os.ReadFile(path)
	g.Expect(err).NotTo(HaveOccurred(), "the evidence manifest is missing")
	var ev evidence
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	g.Expect(dec.Decode(&ev)).To(Succeed(), "the evidence manifest does not parse")
	g.Expect(ev.Schema).To(Equal(1), "unexpected schema")
	g.Expect(ev.Suite).To(Equal("tables-acceptance"))
	g.Expect(ev.Platform.OS).To(Equal(runtime.GOOS), "the evidence is not of this native platform: %+v", ev.Platform)
	g.Expect(ev.Platform.Arch).To(Equal(runtime.GOARCH), "the evidence is not of this native platform: %+v", ev.Platform)
	g.Expect(ev.Platform.Native).To(BeTrue(), "the evidence is not of this native platform: %+v", ev.Platform)
	g.Expect(ev.Publication).To(HavePrefix("none"), "the manifest must say nothing was published")
	joined := make([]string, 0, len(ev.Commands))
	for _, c := range ev.Commands {
		joined = append(joined, strings.Join(c, " "))
	}
	for _, want := range []string{"scripts/addons/package.sh", "go test"} {
		found := false
		for _, c := range joined {
			if strings.Contains(c, want) {
				found = true
			}
		}
		if !found {
			problem("no recorded command contains %q: %v", want, joined)
		}
	}
	digest := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	seen := map[string]string{}
	for _, v := range []string{"0.1.0", "0.2.0"} {
		d, ok := ev.Artifact.Versions[v]
		g.Expect(ok).To(BeTrue(), "no digest of version %s", v)
		for what, value := range map[string]string{"archive": d.Archive, "image": d.Image} {
			if !digest.MatchString(value) {
				problem("%s digest of %s is malformed: %q", what, v, value)
			}
			if other, dup := seen[value]; dup {
				problem("%s digest of %s equals %s", what, v, other)
			}
			seen[value] = v + " " + what
		}
	}
	if ev.Artifact.Addon != "tables" {
		problem("the artifact is %q", ev.Artifact.Addon)
	}

	inventory, err := os.ReadFile(filepath.Join(root, "scripts/addons/suites.json"))
	g.Expect(err).NotTo(HaveOccurred())
	var suites map[string]struct {
		Cases []string `json:"cases"`
	}
	g.Expect(json.Unmarshal(inventory, &suites)).To(Succeed())
	var want, wantBrowser []string
	for _, c := range suites["tables-acceptance"].Cases {
		if name, ok := strings.CutPrefix(c, "go: "); ok {
			want = append(want, name)
		}
		if strings.HasPrefix(c, "tables-acceptance: ") {
			wantBrowser = append(wantBrowser, c)
		}
	}
	var got []string
	for _, c := range ev.Cases {
		got = append(got, c.Name)
		if c.State != "passed" {
			problem("case %q is %s", c.Name, c.State)
			if c.Trace == "" {
				problem("case %q failed and no trace was retained", c.Name)
			} else if _, err := os.Stat(c.Trace); err != nil {
				problem("the trace of %q is missing: %v", c.Name, err)
			}
		}
	}
	var gotBrowser []string
	for _, c := range ev.Browser {
		gotBrowser = append(gotBrowser, c.Name)
		if c.State != "passed" {
			problem("browser case %q is %s", c.Name, c.State)
			if c.Trace == "" {
				problem("browser case %q failed and no trace was retained", c.Name)
			} else if _, err := os.Stat(c.Trace); err != nil {
				problem("the trace of %q is missing: %v", c.Name, err)
			}
		}
	}
	sort.Strings(wantBrowser)
	sort.Strings(gotBrowser)
	if strings.Join(wantBrowser, "\n") != strings.Join(gotBrowser, "\n") {
		problem("the browser cases of the manifest differ from suites.json:\nmanifest: %q\ninventory: %q", gotBrowser, wantBrowser)
	}
	if len(wantBrowser) == 0 {
		problem("the inventory has no browser case")
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		problem("the case inventory of the manifest differs from suites.json:\nmanifest: %q\ninventory: %q", got, want)
	}
	if len(want) == 0 {
		problem("the inventory has no Go case")
	}
}

// A relative evidence path resolves against the package directory the test
// binary runs in, which once left a stale manifest inside the tree. The suite
// refuses it, whatever the writer or the checker.
func TestEvidencePathMustBeAbsolute(t *testing.T) {
	g := NewWithT(t)
	_, err := evidenceLocation(".cache/addons/tables-acceptance-evidence.json", "/repo")
	g.Expect(err).To(HaveOccurred(), "a relative evidence path must be refused")
	_, err = evidenceLocation("../evidence.json", "/repo")
	g.Expect(err).To(HaveOccurred(), "a parent-relative evidence path must be refused")
	p, err := evidenceLocation("", "/repo")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(p).To(Equal("/repo/.cache/addons/tables-acceptance-evidence.json"), "the default is under the repository cache")
	p, err = evidenceLocation("/work/evidence.json", "/repo")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(p).To(Equal("/work/evidence.json"), "an absolute path is used as given")
}
