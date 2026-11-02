// SPDX-License-Identifier: MIT
package agents

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = Describe("selection revisions after failed agent persistence", func() {
	It("does not publish a token or record on failed Put or Delete", func() {
		ctx := context.Background()
		base, err := NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		s := base.(*jsonStore)
		defer s.Close()
		a := Agent{ID: "a", Principal: "u", Name: "A", Addons: []string{"one"}}
		Expect(s.Put(ctx, a)).To(Succeed())
		_, old, err := s.SelectionSnapshot(ctx, "u", "a")
		Expect(err).NotTo(HaveOccurred())
		blocked := filepath.Join(GinkgoT().TempDir(), "blocked")
		Expect(os.WriteFile(blocked, []byte("file"), 0600)).To(Succeed())
		originalPath := s.path
		s.path = filepath.Join(blocked, "agents.json")
		changed := a
		changed.Addons = []string{"two"}
		Expect(s.Put(ctx, changed)).To(HaveOccurred())
		Expect(s.Delete(ctx, "u", "a")).To(HaveOccurred())
		s.path = originalPath
		got, now, err := s.SelectionSnapshot(ctx, "u", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(now).To(Equal(old))
		Expect(got).To(Equal(a))
	})
})
