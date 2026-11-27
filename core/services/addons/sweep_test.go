// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// leftover writes a committed package that no journal names, as a failed
// upgrade leaves it: a receipt, a nested folder, everything read only.
func leftover(packagesRoot, name, digest string) string {
	GinkgoHelper()
	dir := filepath.Join(packagesRoot, name)
	Expect(os.MkdirAll(filepath.Join(dir, "bin"), 0700)).To(Succeed())
	b, e := json.Marshal(packages.PackageReceipt{Digest: digest, Directory: dir, Manifest: contract.Manifest{ID: "sample", Version: "9.9.9"}})
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), b, 0400)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "bin", "sample"), []byte("binary"), 0500)).To(Succeed())
	Expect(os.Chmod(filepath.Join(dir, "bin"), 0500)).To(Succeed())
	Expect(os.Chmod(dir, 0500)).To(Succeed())
	return dir
}

func writeRecoveryFile(cfg OwnerConfig, name, body string) {
	GinkgoHelper()
	dir := filepath.Join(cfg.Root, "recovery", "sample")
	Expect(os.MkdirAll(dir, 0700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, name), []byte(body), 0600)).To(Succeed())
}

var _ = Describe("Startup sweep of unreferenced packages", func() {
	// installed returns an owner with generation 1 and 2 of the sample
	// add-on committed and journaled, closed so that a restart follows.
	installed := func() (OwnerConfig, *Manager, Status) {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("one", "install"))
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Submit(ctx, actor, command("two", "upgrade"))
		Expect(e).NotTo(HaveOccurred())
		s, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Generations).To(HaveLen(2))
		Expect(m.Close()).To(Succeed())
		return cfg, open(cfg, c, i), s
	}

	It("removes the packages of failed candidates and keeps the current and the previous generation", func() {
		cfg, m, s := installed()
		root := filepath.Join(cfg.Root, "packages")
		first := leftover(root, "package-stage-101", "sha256:aa")
		second := leftover(root, "package-stage-102", "sha256:bb")
		Expect(m.Reconcile(ctx)).To(Succeed())
		Expect(first).NotTo(BeAnExistingFile())
		Expect(second).NotTo(BeAnExistingFile())
		for _, g := range s.Generations {
			Expect(filepath.Join(g.Receipt.Directory, "receipt.json")).To(BeAnExistingFile())
		}
	})

	It("keeps a package that a data recovery journal names", func() {
		cfg, m, _ := installed()
		root := filepath.Join(cfg.Root, "packages")
		candidate := leftover(root, "package-stage-201", "sha256:cand")
		other := leftover(root, "package-stage-202", "sha256:other")
		writeRecoveryFile(cfg, "journal.json", `{"kind":"upgrade","phase":"migrating","id":"b0001","digest":"sha256:cand","number":3,"started":"2026-01-01T00:00:00Z"}`)
		Expect(m.Reconcile(ctx)).To(Succeed())
		Expect(candidate).To(BeADirectory())
		Expect(other).NotTo(BeAnExistingFile())
	})

	It("keeps the package of a canceled upgrade that the same operation can continue", func() {
		cfg, m, _ := installed()
		canceled := leftover(filepath.Join(cfg.Root, "packages"), "package-stage-301", "sha256:cc")
		m.canceled = map[string]packages.PackageReceipt{"three": {Directory: canceled}}
		Expect(m.Reconcile(ctx)).To(Succeed())
		Expect(canceled).To(BeADirectory())
	})

	DescribeTable("removes nothing when a recovery file cannot be read",
		func(name, body string) {
			cfg, m, _ := installed()
			orphan := leftover(filepath.Join(cfg.Root, "packages"), "package-stage-401", "sha256:dd")
			writeRecoveryFile(cfg, name, body)
			Expect(m.Reconcile(ctx)).To(Succeed())
			Expect(orphan).To(BeADirectory())
		},
		Entry("a corrupt journal", "journal.json", "{not json"),
		Entry("a journal without a kind", "journal.json", `{"phase":"begin"}`),
		Entry("a corrupt state", "state.json", "{not json"),
	)

	It("removes nothing when a journaled package is outside the package folder", func() {
		cfg, m, _ := installed()
		orphan := leftover(filepath.Join(cfg.Root, "packages"), "package-stage-501", "sha256:ee")
		m.mu.Lock()
		st := m.journal.Addons["sample"]
		st.Generations = append(st.Generations, Generation{Number: 9, Receipt: packages.PackageReceipt{Directory: GinkgoT().TempDir()}})
		m.journal.Addons["sample"] = st
		Expect(m.persistJournal("test")).To(Succeed())
		m.mu.Unlock()
		Expect(m.Reconcile(ctx)).To(Succeed())
		Expect(orphan).To(BeADirectory())
	})

	It("does not follow a link out of the package folder", func() {
		if goruntime.GOOS == "windows" {
			Skip("symbolic links need privileges on Windows")
		}
		cfg, m, _ := installed()
		root := filepath.Join(cfg.Root, "packages")
		outside := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(outside, "keep"), 0500)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0400)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(filepath.Join(outside, "keep"), 0700) })
		Expect(os.Symlink(outside, filepath.Join(root, "package-stage-601"))).To(Succeed())
		orphan := leftover(root, "package-stage-602", "sha256:ff")
		Expect(os.Chmod(orphan, 0700)).To(Succeed())
		Expect(os.Symlink(outside, filepath.Join(orphan, "escape"))).To(Succeed())
		Expect(os.Chmod(orphan, 0500)).To(Succeed())
		Expect(m.Reconcile(ctx)).To(Succeed())
		Expect(orphan).NotTo(BeAnExistingFile())
		Expect(filepath.Join(outside, "file")).To(BeAnExistingFile())
		info, e := os.Stat(filepath.Join(outside, "keep"))
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0500)))
	})
})
