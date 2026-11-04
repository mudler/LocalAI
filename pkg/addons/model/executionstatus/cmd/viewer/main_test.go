// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	es "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestViewer(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Execution status viewer") }

var _ = Describe("Real reducer trace export", func() {
	It("propagates output failures for checks and writes", func() {
		path := filepath.Join(GinkgoT().TempDir(), "viewer.html")
		Expect(run([]string{"-check"}, failedOutput{})).To(MatchError("output failed"))
		Expect(run([]string{"-o", path}, failedOutput{})).To(MatchError("output failed"))
	})
	It("exports every trace step with state, event, effects, rule and guard", func() {
		d, err := collect(es.Traces(), es.RunTrace, es.Explore)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Traces).To(HaveLen(len(es.Traces())))
		for i, tr := range d.Traces {
			Expect(tr.Steps).To(HaveLen(len(es.Traces()[i].Events)))
			for j, s := range tr.Steps {
				Expect(s.Number).To(Equal(j + 1))
				Expect(s.Event).NotTo(BeEmpty())
				Expect(s.Before).NotTo(BeEmpty())
				Expect(s.After).NotTo(BeEmpty())
				Expect(s.Guard).NotTo(BeEmpty())
				Expect(s.Rule).To(Equal(es.Traces()[i].Want[j].Rule))
				Expect(s.Effects).NotTo(BeNil())
			}
		}
		Expect(d.Explore.Cells).To(HaveLen(49))
	})
	It("rejects wrong expected rules", func() {
		tr := es.Traces()
		tr[0].Want[0].Rule = es.I
		_, err := collect(tr, es.RunTrace, es.Explore)
		Expect(err).To(MatchError(ContainSubstring("step 1")))
	})
	It("rejects wrong expected states", func() {
		tr := es.Traces()
		tr[0].Want[0].State = es.Closed{}
		_, err := collect(tr, es.RunTrace, es.Explore)
		Expect(err).To(HaveOccurred())
	})
	It("rejects missing ordered effects", func() {
		tr := es.Traces()
		tr[0].Want[1].Effects = nil
		_, err := collect(tr, es.RunTrace, es.Explore)
		Expect(err).To(HaveOccurred())
	})
	It("rejects wrong final projection", func() {
		tr := es.Traces()
		tr[0].Value = es.Unknown()
		_, err := collect(tr, es.RunTrace, es.Explore)
		Expect(err).To(HaveOccurred())
	})
	It("rejects missing trace families", func() { _, err := collect(es.Traces()[:1], es.RunTrace, es.Explore); Expect(err).To(HaveOccurred()) })
	It("propagates invariant counterexamples", func() {
		_, err := collect(es.Traces(), es.RunTrace, func(int) (es.Exploration, error) { return es.Exploration{}, errors.New("counterexample") })
		Expect(err).To(MatchError(ContainSubstring("counterexample")))
	})
	It("rejects incomplete exploration", func() {
		_, err := collect(es.Traces(), es.RunTrace, func(int) (es.Exploration, error) { return es.Exploration{}, nil })
		Expect(err).To(HaveOccurred())
	})
	It("generates deterministic self-contained HTML and detects embedded data drift", func() {
		a, err := generate()
		Expect(err).NotTo(HaveOccurred())
		b, err := generate()
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(b))
		Expect(validatePage(a)).To(Succeed())
		Expect(validatePage(bytes.Replace(a, []byte(`"R1"`), []byte(`"R8"`), 1))).NotTo(Succeed())
		Expect(string(a)).To(ContainSubstring(`id="trace"`))
		Expect(string(a)).To(ContainSubstring(`id="next"`))
	})
	It("writes explicit output outside the working directory", func() {
		cwd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.Chdir, cwd)
		work := GinkgoT().TempDir()
		root := GinkgoT().TempDir()
		Expect(os.Chdir(work)).To(Succeed())
		GinkgoT().Setenv("SDD_LOCAL_ROOT", root)
		var out bytes.Buffer
		Expect(run([]string{"-o", filepath.Join(root, "executionstatus-viewer.html")}, &out)).To(Succeed())
		html, err := os.ReadFile(filepath.Join(root, "executionstatus-viewer.html"))
		Expect(err).NotTo(HaveOccurred())
		Expect(validatePage(html)).To(Succeed())
		entries, err := os.ReadDir(work)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})
	It("defaults to portable viewer.html", func() {
		cwd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.Chdir, cwd)
		Expect(os.Chdir(GinkgoT().TempDir())).To(Succeed())
		GinkgoT().Setenv("SDD_LOCAL_ROOT", "")
		Expect(os.Unsetenv("SDD_LOCAL_ROOT")).To(Succeed())
		var out bytes.Buffer
		Expect(run(nil, &out)).To(Succeed())
		html, err := os.ReadFile("viewer.html")
		Expect(err).NotTo(HaveOccurred())
		Expect(validatePage(html)).To(Succeed())
	})
	It("keeps bare checks write-free even with an unusable default root", func() {
		root := GinkgoT().TempDir()
		GinkgoT().Setenv("SDD_LOCAL_ROOT", filepath.Join(root, "missing"))
		var out bytes.Buffer
		Expect(run([]string{"-check"}, &out)).To(Succeed())
		entries, err := os.ReadDir(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})
	It("checks in memory without writing and supports explicit output plus stale-file checks", func() {
		GinkgoT().Setenv("SDD_LOCAL_ROOT", filepath.Join(GinkgoT().TempDir(), "missing"))
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "viewer.html")
		var out bytes.Buffer
		Expect(run([]string{"-check"}, &out)).To(Succeed())
		Expect(run([]string{"-o", path}, &out)).To(Succeed())
		Expect(run([]string{"-check", "-o", path}, &out)).To(Succeed())
		Expect(os.WriteFile(path, []byte("stale"), 0600)).To(Succeed())
		Expect(run([]string{"-check", "-o", path}, &out)).NotTo(Succeed())
	})
	It("reports missing check targets, write failures, bad flags and positional arguments", func() {
		var out bytes.Buffer
		path := filepath.Join(GinkgoT().TempDir(), "missing", "viewer.html")
		Expect(run([]string{"-check", "-o", path}, &out)).NotTo(Succeed())
		Expect(run([]string{"-o", path}, &out)).NotTo(Succeed())
		Expect(run([]string{"-bad"}, &out)).NotTo(Succeed())
		Expect(run([]string{"unexpected"}, &out)).NotTo(Succeed())
	})
})

type failedOutput struct{}

func (failedOutput) Write([]byte) (int, error) { return 0, errors.New("output failed") }
