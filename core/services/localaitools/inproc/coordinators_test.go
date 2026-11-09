package inproc

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

var _ = Describe("Coordinators", func() {
	ctx := context.Background()

	It("reports that the chats or the coordinators are off", func() {
		c := &Client{}
		_, err := c.ListCoordinators(ctx)
		Expect(err).To(MatchError(errChatsOff))
	})

	It("lists, pauses and resumes through the host and records the acting administrator", func() {
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ws.Close() })
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(),
			Harness: studiotest.NewGateHarness(), Files: files.New(ws), Agents: ag})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		c := &Client{Chats: func() *studio.Service { return svc }}

		_, err = c.ListCoordinators(ctx)
		Expect(err).To(MatchError(errCoordinatorsOff))

		cs, err := coordinator.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := studio.NewCoordinators(svc, studio.CoordinatorOptions{Store: cs})
		Expect(err).NotTo(HaveOccurred())
		svc.SetCoordinators(host)
		v, err := host.Create(ctx, "alice", studio.CoordinatorInput{Agent: agents.Agent{Name: "Ops", Model: "m"}})
		Expect(err).NotTo(HaveOccurred())

		list, err := c.ListCoordinators(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0]).To(MatchFields(IgnoreExtras, Fields{"ID": Equal(v.Agent.ID), "Owner": Equal("alice"), "Name": Equal("Ops"), "Paused": BeFalse()}))

		admin := localaitools.WithUser(ctx, &auth.User{ID: "root", Role: auth.RoleAdmin})
		got, err := c.PauseCoordinator(admin, v.Agent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Paused).To(BeTrue())
		Expect(got.PausedBy).To(Equal("root"))

		rows, err := c.ListCoordinatorAudit(admin, localaitools.CoordinatorAuditQuery{Kind: coordinator.KindPause})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Actor).To(Equal("root"))
		Expect(rows[0].Owner).To(Equal("alice"))

		got, err = c.ResumeCoordinator(admin, v.Agent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Paused).To(BeFalse())
		_, err = c.PauseCoordinator(admin, "nope")
		Expect(err).To(MatchError(studio.ErrNotACoordinator))
	})
})
