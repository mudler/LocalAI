package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the window check of a chat", func() {
	ctx := context.Background()

	// check opens a chat of user on a model with the given window and
	// returns the check. chatSetting is the chat's own context size (0 = none).
	check := func(user string, window, chatSetting int) studio.Harness {
		svc, _ := newToolService(sayOK, func(d *studio.Deps) {
			d.IsAdmin = func(_ context.Context, id string) bool { return id == "root" }
			d.ContextTokens = func(string) int { return window }
		})
		rec, err := svc.Create(ctx, user, studio.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		if chatSetting > 0 {
			rec, err = svc.Update(ctx, user, rec.ID, studio.UpdateInput{Settings: &session.ChatSettings{ContextSize: chatSetting}})
			Expect(err).NotTo(HaveOccurred())
		}
		return svc.Harness(ctx, user, rec)
	}

	Describe("for a regular user", func() {
		It("is not OK below 16384 and cannot be fixed by them", func() {
			h := check("bob", 8192, 0)
			Expect(h.OK).To(BeFalse())
			Expect(h.Window).To(Equal(8192))
			Expect(h.Minimum).To(Equal(16384))
			Expect(h.Recommended).To(Equal(32768))
			Expect(h.CanFix).To(BeFalse(), "only an administrator changes a model")
			Expect(h.Admin).To(BeFalse())
			Expect(h.Source).To(Equal(studio.HarnessSourceModel))
		})
		It("is OK at 16384", func() {
			h := check("bob", 16384, 0)
			Expect(h.OK).To(BeTrue())
			Expect(h.Source).To(BeEmpty())
		})
		It("is OK at the recommended window", func() {
			Expect(check("bob", 32768, 0).OK).To(BeTrue())
		})
	})

	Describe("for an administrator", func() {
		It("needs 24576 and may fix the model", func() {
			h := check("root", 16384, 0)
			Expect(h.OK).To(BeFalse())
			Expect(h.Minimum).To(Equal(24576))
			Expect(h.Recommended).To(Equal(32768))
			Expect(h.CanFix).To(BeTrue())
			Expect(h.Admin).To(BeTrue())
			Expect(h.Source).To(Equal(studio.HarnessSourceModel))
		})
		It("is OK at 24576", func() {
			Expect(check("root", 24576, 0).OK).To(BeTrue())
		})
	})

	It("says nothing against a model whose window is not known", func() {
		h := check("bob", 0, 0)
		Expect(h.OK).To(BeTrue())
		Expect(h.Window).To(BeZero())
	})

	It("tells an own chat setting below the minimum apart from the model", func() {
		h := check("bob", 65536, 8192)
		Expect(h.OK).To(BeFalse())
		Expect(h.Source).To(Equal(studio.HarnessSourceChat))
		Expect(h.Window).To(Equal(8192))
		Expect(h.CanFix).To(BeTrue(), "the owner changes their own chat")
	})

	It("does not let a large chat setting hide a model that is too small", func() {
		h := check("bob", 8192, 65536)
		Expect(h.OK).To(BeFalse())
		Expect(h.Source).To(Equal(studio.HarnessSourceModel))
		Expect(h.Window).To(Equal(8192))
	})

	It("treats everyone as the administrator when authentication is off", func() {
		svc, _ := newToolService(sayOK, func(d *studio.Deps) { d.ContextTokens = func(string) int { return 16384 } })
		rec, err := svc.Create(ctx, "", studio.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		h := svc.Harness(ctx, "", rec)
		Expect(h.Admin).To(BeTrue())
		Expect(h.Minimum).To(Equal(24576))
		Expect(h.CanFix).To(BeTrue())
	})
})
