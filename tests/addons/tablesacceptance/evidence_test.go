// SPDX-License-Identifier: MIT
package tablesacceptance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
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
		t.Skip("runs after the suite, from scripts/addons/tables_acceptance.sh")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	path := os.Getenv("TABLES_ACCEPTANCE_EVIDENCE")
	if path == "" {
		path = filepath.Join(root, ".cache/addons/tables-acceptance-evidence.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the evidence manifest is missing: %v", err)
	}
	var ev evidence
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("the evidence manifest does not parse: %v", err)
	}
	if ev.Schema != 1 || ev.Suite != "tables-acceptance" {
		t.Fatalf("unexpected schema or suite: %d %q", ev.Schema, ev.Suite)
	}
	if ev.Platform.OS != runtime.GOOS || ev.Platform.Arch != runtime.GOARCH || !ev.Platform.Native {
		t.Fatalf("the evidence is not of this native platform: %+v", ev.Platform)
	}
	if !strings.HasPrefix(ev.Publication, "none") {
		t.Fatalf("the manifest must say nothing was published: %q", ev.Publication)
	}
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
			t.Errorf("no recorded command contains %q: %v", want, joined)
		}
	}
	digest := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	seen := map[string]string{}
	for _, v := range []string{"0.1.0", "0.2.0"} {
		d, ok := ev.Artifact.Versions[v]
		if !ok {
			t.Fatalf("no digest of version %s", v)
		}
		for what, value := range map[string]string{"archive": d.Archive, "image": d.Image} {
			if !digest.MatchString(value) {
				t.Errorf("%s digest of %s is malformed: %q", what, v, value)
			}
			if other, dup := seen[value]; dup {
				t.Errorf("%s digest of %s equals %s", what, v, other)
			}
			seen[value] = v + " " + what
		}
	}
	if ev.Artifact.Addon != "tables" {
		t.Errorf("the artifact is %q", ev.Artifact.Addon)
	}

	inventory, err := os.ReadFile(filepath.Join(root, "scripts/addons/suites.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suites map[string]struct {
		Cases []string `json:"cases"`
	}
	if err := json.Unmarshal(inventory, &suites); err != nil {
		t.Fatal(err)
	}
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
			t.Errorf("case %q is %s", c.Name, c.State)
			if c.Trace == "" {
				t.Errorf("case %q failed and no trace was retained", c.Name)
			} else if _, err := os.Stat(c.Trace); err != nil {
				t.Errorf("the trace of %q is missing: %v", c.Name, err)
			}
		}
	}
	var gotBrowser []string
	for _, c := range ev.Browser {
		gotBrowser = append(gotBrowser, c.Name)
		if c.State != "passed" {
			t.Errorf("browser case %q is %s", c.Name, c.State)
			if c.Trace == "" {
				t.Errorf("browser case %q failed and no trace was retained", c.Name)
			} else if _, err := os.Stat(c.Trace); err != nil {
				t.Errorf("the trace of %q is missing: %v", c.Name, err)
			}
		}
	}
	sort.Strings(wantBrowser)
	sort.Strings(gotBrowser)
	if strings.Join(wantBrowser, "\n") != strings.Join(gotBrowser, "\n") {
		t.Errorf("the browser cases of the manifest differ from suites.json:\nmanifest: %q\ninventory: %q", gotBrowser, wantBrowser)
	}
	if len(wantBrowser) == 0 {
		t.Error("the inventory has no browser case")
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Errorf("the case inventory of the manifest differs from suites.json:\nmanifest: %q\ninventory: %q", got, want)
	}
	if len(want) == 0 {
		t.Error("the inventory has no Go case")
	}
}
