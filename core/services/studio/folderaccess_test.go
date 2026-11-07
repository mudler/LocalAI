// SPDX-License-Identifier: MIT
package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// folderPeople is an account directory of alice, bob and carol.
type folderPeople struct{}

func (folderPeople) Accounts(context.Context, string, string, int) ([]sharedfolders.User, error) {
	return nil, nil
}

func (folderPeople) Active(_ context.Context, id string) (sharedfolders.User, error) {
	if name, ok := shareUsers[id]; ok {
		return sharedfolders.User{ID: id, Name: name}, nil
	}
	return sharedfolders.User{}, sharedfolders.ErrUnknownUser
}

var _ = Describe("a folder an owner shares from their home", func() {
	ctx := context.Background()

	// rig runs bob's and carol's turns until release closes, with alice's
	// folder Projects shared with bob at mode.
	rig := func(release chan struct{}, mode workspace.Mode) (*studio.Service, int64, string, string) {
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "long" {
				return []studiotest.ToolStep{{Block: release}, {Say: "late"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) {
			d.Files = newFiles()
			d.FolderDirectory = folderPeople{}
		})
		f := svc.Files()
		for _, u := range []string{alice, bob, carol} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
		n, err := f.Store().Mkdir(ctx, f.Actor(alice), "/users/alice/Projects")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.SharedFolders().OwnSetMember(ctx, files.UserActor(alice), n.ID, bob, mode)
		Expect(err).NotTo(HaveOccurred())
		bobs, carols := create(svc, bob), create(svc, carol)
		sent(svc, bob, bobs, "long")
		sent(svc, carol, carols, "long")
		until("both turns to run", func() bool { return turn(svc, bob, bobs).Running && turn(svc, carol, carols).Running })
		return svc, n.ID, bobs, carols
	}

	It("stops the grantee's running turn before the owner's revoke answers, and leaves the others", func() {
		release := make(chan struct{})
		defer close(release)
		svc, id, bobs, carols := rig(release, workspace.ModeRead)
		_, err := svc.SharedFolders().OwnRemoveMember(ctx, files.UserActor(alice), id, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn(svc, bob, bobs).Running).To(BeFalse(), "the revoke answers only once the turn stopped")
		Expect(turn(svc, bob, bobs).Last).To(Equal(store.TurnStopped))
		Expect(turn(svc, carol, carols).Running).To(BeTrue(), "a turn without the folder runs on")
	})

	It("stops the grantee's running turn when the owner lowers write to read, and not when the owner raises it", func() {
		release := make(chan struct{})
		defer close(release)
		svc, id, bobs, _ := rig(release, workspace.ModeRead)
		_, err := svc.SharedFolders().OwnSetMember(ctx, files.UserActor(alice), id, bob, workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn(svc, bob, bobs).Running).To(BeTrue(), "more access stops nothing")

		other := make(chan struct{})
		defer close(other)
		svc2, id2, bobs2, _ := rig(other, workspace.ModeWrite)
		_, err = svc2.SharedFolders().OwnSetMember(ctx, files.UserActor(alice), id2, bob, workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn(svc2, bob, bobs2).Running).To(BeFalse())
		Expect(turn(svc2, bob, bobs2).Last).To(Equal(store.TurnStopped))
	})

	It("stops every account's turn that holds the folder when the owner revokes everyone", func() {
		release := make(chan struct{})
		defer close(release)
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "long" {
				return []studiotest.ToolStep{{Block: release}, {Say: "late"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) {
			d.Files = newFiles()
			d.FolderDirectory = folderPeople{}
		})
		f := svc.Files()
		for _, u := range []string{alice, bob, carol} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
		n, err := f.Store().Mkdir(ctx, f.Actor(alice), "/users/alice/Team")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.SharedFolders().OwnSetMember(ctx, files.UserActor(alice), n.ID, sharedfolders.Everyone, workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		bobs, carols := create(svc, bob), create(svc, carol)
		sent(svc, bob, bobs, "long")
		sent(svc, carol, carols, "long")
		until("both turns to run", func() bool { return turn(svc, bob, bobs).Running && turn(svc, carol, carols).Running })
		_, err = svc.SharedFolders().OwnRemoveMember(ctx, files.UserActor(alice), n.ID, sharedfolders.Everyone)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn(svc, bob, bobs).Running).To(BeFalse())
		Expect(turn(svc, carol, carols).Running).To(BeFalse())
	})

	It("lists the owner's shared folders from the JSON store too", func() {
		svc, _ := newToolService(sayOK, func(d *studio.Deps) {
			d.Files = newFiles()
			d.FolderDirectory = folderPeople{}
		})
		f := svc.Files()
		Expect(f.EnsureHome(ctx, alice)).To(Succeed())
		Expect(f.EnsureHome(ctx, bob)).To(Succeed())
		n, err := f.Store().Mkdir(ctx, f.Actor(alice), "/users/alice/Projects/Inner")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.SharedFolders().OwnSetMember(ctx, files.UserActor(alice), n.ID, bob, workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		owned, err := svc.SharedFolders().Owned(ctx, files.UserActor(alice))
		Expect(err).NotTo(HaveOccurred())
		Expect(owned).To(ConsistOf(And(HaveField("ID", n.ID), HaveField("Path", "/Projects/Inner"),
			HaveField("Members", ConsistOf(HaveField("Name", "Bob"))))))
	})
})
