package studio_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("the access of an add-on call", func() {
	It("gives a call that no chat made the user's whole home", func() {
		r := newAccessRig()
		x, err := r.svc.AddonAccess(r.ctx, alice, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(x.Scoped()).To(BeFalse())
		Expect(x.Actor().ChatID).To(BeEmpty())
		Expect(x.Place().Primary()).To(Equal(r.home))
	})

	It("makes the home of a user that has none yet", func() {
		r := newAccessRig()
		x, err := r.svc.AddonAccess(r.ctx, "carol", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(x.Place().Primary()).To(Equal(session.HomePath("carol")))
	})

	It("gives a chat's call the chat's access", func() {
		r := newAccessRig()
		acme := r.mkdir("/Acme")
		rec := r.chat(r.agent("/Acme", "read").ID)
		x, err := r.svc.AddonAccess(r.ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(x.Scoped()).To(BeTrue())
		Expect(x.Actor().ChatID).To(Equal(rec.ID))
		Expect(x.Place().Primary()).To(Equal(acme.Path))
		Expect(x.Place().PrimaryWritable()).To(BeFalse())
		want := r.access(rec.ID)
		Expect(x.Actor().SameFence(want.Actor())).To(BeTrue())

		plain := r.chat("")
		x, err = r.svc.AddonAccess(r.ctx, alice, plain.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(x.Scoped()).To(BeFalse())
		Expect(x.Actor().ChatID).To(Equal(plain.ID))
	})

	It("refuses a chat that does not exist, of another user, or whose folder is gone", func() {
		r := newAccessRig()
		_, err := r.svc.AddonAccess(r.ctx, alice, "no-such-chat")
		Expect(err).To(HaveOccurred())
		rec := r.chat("")
		_, err = r.svc.AddonAccess(r.ctx, "bob", rec.ID)
		Expect(err).To(HaveOccurred())
		gone := r.mkdir("/Gone")
		scoped := r.chat(r.agent("/Gone", "write").ID)
		Expect(r.ws.Delete(r.ctx, r.user, gone.Path)).To(Succeed())
		_, err = r.svc.AddonAccess(r.ctx, alice, scoped.ID)
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing), "never the home")
	})
})
