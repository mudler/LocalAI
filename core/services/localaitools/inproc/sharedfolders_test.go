package inproc

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

var _ = Describe("Shared folders", func() {
	ctx := context.Background()

	It("reports that the chats are off without the service", func() {
		c := &Client{}
		_, err := c.ListSharedFolders(ctx)
		Expect(err).To(MatchError(errChatsOff))
		c.SharedFolders = func() *sharedfolders.Service { return nil }
		_, err = c.CreateSharedFolder(ctx, "Team")
		Expect(err).To(MatchError(errChatsOff))
	})

	It("manages a folder through the service, as the assistant", func() {
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ws.Close() })
		svc := sharedfolders.New(sharedfolders.Options{Files: files.New(ws)})
		c := &Client{SharedFolders: func() *sharedfolders.Service { return svc }}

		f, err := c.CreateSharedFolder(ctx, "Team")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Path).To(Equal("/shared/Team"))
		Expect(f.Members).To(BeEmpty(), "the assistant is not a member")

		f, err = c.SetSharedFolderMember(ctx, f.ID, "@everyone", "write")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Members).To(HaveLen(1))
		Expect(f.Members[0].Mode).To(Equal("write"))
		_, err = c.SetSharedFolderMember(ctx, f.ID, "@everyone", "owner")
		Expect(err).To(MatchError(sharedfolders.ErrBadMode))

		f, err = c.RenameSharedFolder(ctx, f.ID, "Crew")
		Expect(err).NotTo(HaveOccurred())
		removed, err := c.DeleteSharedFolder(ctx, f.ID)
		Expect(err).NotTo(HaveOccurred())
		list, err := c.ListSharedFolders(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
		back, err := c.RestoreSharedFolder(ctx, removed.UndoRev)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Name).To(Equal("Crew"))
		back, err = c.RemoveSharedFolderMember(ctx, back.ID, "@everyone")
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Members).To(BeEmpty())

		evs, err := c.ListSharedFolderEvents(ctx, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(6))
		Expect(evs[0].Action).To(Equal("revoke"))
		Expect(evs[0].Actor).To(Equal(sharedfolders.ActorAssistant))
	})
})
