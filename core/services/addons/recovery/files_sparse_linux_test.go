//go:build linux

// SPDX-License-Identifier: MIT
package recovery_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Sparse files in a backup", func() {
	It("keeps a sparse file sparse, with the same bytes", func() {
		r := newRig(policy.ModeNamedUser, "admin")
		dir := filepath.Join(r.root, "data", addon)
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		src := filepath.Join(dir, "overlay.img")
		f, e := os.OpenFile(src, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		Expect(e).NotTo(HaveOccurred())
		_, e = f.WriteAt([]byte("head"), 0)
		Expect(e).NotTo(HaveOccurred())
		_, e = f.WriteAt([]byte("tail"), 64<<20)
		Expect(e).NotTo(HaveOccurred())
		Expect(f.Truncate(128 << 20)).To(Succeed())
		Expect(f.Close()).To(Succeed())
		var live syscall.Stat_t
		Expect(syscall.Stat(src, &live)).To(Succeed())
		Expect(live.Blocks*512).To(BeNumerically("<", live.Size/8), "the seeded file is sparse")

		rc, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		dst := filepath.Join(r.root, "backups", addon, rc.ID, "data", "overlay.img")
		var copySt syscall.Stat_t
		Expect(syscall.Stat(dst, &copySt)).To(Succeed())
		Expect(copySt.Size).To(Equal(live.Size))
		Expect(copySt.Blocks).To(BeNumerically("<=", live.Blocks), "backup allocation")
		Expect(copySt.Blocks*512).To(BeNumerically("<", live.Size/8), "backup allocation")

		got, e := os.ReadFile(dst)
		Expect(e).NotTo(HaveOccurred())
		want, e := os.ReadFile(src)
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(Equal(want), "backup bytes")
	})
})
