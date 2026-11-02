// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = ginkgo.Describe("selection revisions after failed chat persistence", func() {
	ginkgo.It("does not publish a token or record on failed edits or deletion", func() {
		ctx := context.Background()
		base, err := NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		s := base.(*jsonStore)
		a := session.ChatRecord{ID: "a", Principal: "u", Title: "A"}
		Expect(s.CreateChat(ctx, a)).To(Succeed())
		_, old, err := s.SelectionSnapshot(ctx, "u", "a")
		Expect(err).NotTo(HaveOccurred())
		blocked := filepath.Join(ginkgo.GinkgoT().TempDir(), "blocked")
		Expect(os.WriteFile(blocked, []byte("file"), 0600)).To(Succeed())
		s.dir = blocked
		Expect(s.SetAddonsOff(ctx, "u", "a", []string{"two"})).To(HaveOccurred())
		Expect(s.SetHostConsent(ctx, "u", "a", &session.HostConsent{ID: "new"})).To(HaveOccurred())
		Expect(s.DeleteChat(ctx, "u", "a")).To(HaveOccurred())
		s.dir = ""
		got, now, err := s.SelectionSnapshot(ctx, "u", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(now).To(Equal(old))
		Expect(got).To(Equal(a))
	})
})
