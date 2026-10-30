package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("attachments as the person reads them", func() {
	It("marks a file that is gone and follows a renamed chat folder", func() {
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, newFiles())
		id := create(svc, alice)
		_, err := svc.SendWithFiles(context.Background(), alice, id, "two", []session.Upload{upload("keep.txt", "K"), upload("gone.txt", "G")})
		Expect(err).NotTo(HaveOccurred())
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		h.Gate <- struct{}{}
		idle(svc, alice, id)
		folder := chatFolder(svc)
		ws := svc.Files().Store()
		Expect(ws.Delete(context.Background(), workspace.Actor{Principal: alice}, session.HomePath(alice)+folder+"/gone.txt")).To(Succeed())
		title := "Renamed"
		_, err = svc.Update(context.Background(), alice, id, studio.UpdateInput{Title: &title})
		Expect(err).NotTo(HaveOccurred())
		renamed := chatFolder(svc)
		Expect(renamed).NotTo(Equal(folder))

		es, err := svc.Messages(context.Background(), alice, id, 0, 0)
		Expect(err).NotTo(HaveOccurred())
		lines := svc.ResolveAttachments(context.Background(), alice, id, es)
		var atts []session.Attachment
		for _, e := range lines {
			atts = append(atts, e.Attachments...)
		}
		Expect(atts).To(HaveLen(2))
		Expect(atts[0].Name).To(Equal("keep.txt"))
		Expect(atts[0].Path).To(Equal(renamed + "/keep.txt"))
		Expect(atts[0].Missing).To(BeFalse())
		Expect(atts[1].Name).To(Equal("gone.txt"))
		Expect(atts[1].Missing).To(BeTrue())
	})
})
