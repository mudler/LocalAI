package gormstore

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

var _ = Describe("VerifyBlobVolume", func() {
	ctx := context.Background()
	var (
		db   *gorm.DB
		root string
	)
	// node opens a store the way one frontend would. SQLite stands in for
	// PostgreSQL, so the shared-filesystem mode is switched on by hand.
	node := func(dir string) *Workspace {
		GinkgoHelper()
		fs, err := blob.NewFS(dir)
		Expect(err).ToNot(HaveOccurred())
		s, err := New(db, fs, Options{Admin: "admin"})
		Expect(err).ToNot(HaveOccurred())
		s.sharedFilesystem = true
		return s
	}

	BeforeEach(func() {
		var err error
		db, err = auth.OpenSQLiteFile(filepath.Join(GinkgoT().TempDir(), "ws.db"))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		root = filepath.Join(GinkgoT().TempDir(), "blobs")
	})

	It("names a new volume and accepts it again on every node that mounts it", func() {
		Expect(node(root).VerifyBlobVolume(ctx)).To(Succeed())
		Expect(node(root).VerifyBlobVolume(ctx)).To(Succeed())
	})

	It("refuses a node whose blob folder is another volume", func() {
		Expect(node(root).VerifyBlobVolume(ctx)).To(Succeed())
		other := node(filepath.Join(GinkgoT().TempDir(), "other"))
		Expect(other.VerifyBlobVolume(ctx)).To(MatchError(ErrVolumeMismatch))
	})

	It("refuses a node whose folder has another identity", func() {
		first := node(filepath.Join(GinkgoT().TempDir(), "a"))
		Expect(first.VerifyBlobVolume(ctx)).To(Succeed())
		// A second volume that was named before it met this database.
		second := node(filepath.Join(GinkgoT().TempDir(), "b"))
		vol := second.blobs.(blob.Volume)
		Expect(vol.WriteVolumeID(ctx, "someone-else")).To(Succeed())
		Expect(second.VerifyBlobVolume(ctx)).To(MatchError(ErrVolumeMismatch))
	})

	It("adopts the identity of a volume when the database has none", func() {
		fs, err := blob.NewFS(root)
		Expect(err).ToNot(HaveOccurred())
		Expect(fs.(blob.Volume).WriteVolumeID(ctx, "kept")).To(Succeed())
		Expect(node(root).VerifyBlobVolume(ctx)).To(Succeed())
		id, err := node(root).volumeID(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(id).To(Equal("kept"))
	})

	It("does nothing without a shared filesystem", func() {
		s := node(root)
		s.sharedFilesystem = false
		Expect(s.VerifyBlobVolume(ctx)).To(Succeed())
		id, err := s.volumeID(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(id).To(BeEmpty())
	})
})
