// SPDX-License-Identifier: MIT
package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("shared filesystem publication", func() {
	for _, stage := range []string{"write", "file sync", "rename", "directory sync"} {
		It("cleans up after injected "+stage+" failure", func() {
			root := GinkgoT().TempDir()
			st, err := NewFS(root)
			Expect(err).NotTo(HaveOccurred())
			s := st.(*fsStore)
			content := "complete"
			seed, err := NewFS(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			sha, _, err := seed.Put(context.Background(), strings.NewReader(content), 0)
			Expect(err).NotTo(HaveOccurred())
			// Pre-create shard/tmp so directory-sync injection tests post-publication.
			Expect(os.MkdirAll(filepath.Dir(shaPath(root, sha)), 0o700)).To(Succeed())
			Expect(os.Mkdir(filepath.Join(root, "tmp"), 0o700)).To(Succeed())
			injected := errors.New("injected " + stage)
			s.fault = func(at string) error {
				if at == stage {
					return injected
				}
				return nil
			}
			_, _, err = s.Put(context.Background(), strings.NewReader(content), 0)
			Expect(err).To(MatchError(injected))
			entries, err := os.ReadDir(filepath.Join(root, "tmp"))
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
			s.fault = nil
			f, err := s.Open(context.Background(), sha)
			if stage == "directory sync" {
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = f.Close() }()
				got, err := io.ReadAll(f)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(got)).To(Equal(content))
			} else {
				if err == nil {
					_ = f.Close()
				}
				Expect(err).To(HaveOccurred(), "failed upload must not be published")
			}
		})
	}

	It("syncs newly created ancestors", func() {
		st, err := NewFS(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		s := st.(*fsStore)
		count := 0
		s.fault = func(at string) error {
			if at == "directory sync" {
				count++
			}
			return nil
		}
		_, _, err = s.Put(context.Background(), strings.NewReader("new shards"), 0)
		Expect(err).NotTo(HaveOccurred())
		// Creation of tmp and of the two shard folders, then the folder that
		// gains the name. A finished file needs no sync of every ancestor.
		Expect(count).To(Equal(4))
	})

	It("preserves concurrent publication after a failed sync", func() {
		root := GinkgoT().TempDir()
		first, err := NewFS(root)
		Expect(err).NotTo(HaveOccurred())
		other, err := NewFS(root)
		Expect(err).NotTo(HaveOccurred())
		content := "concurrent publication"
		sha, _, err := other.Put(context.Background(), strings.NewReader(content), 0)
		Expect(err).NotTo(HaveOccurred())
		injected := errors.New("post-rename sync")
		first.(*fsStore).fault = func(at string) error {
			if at != "directory sync" {
				return nil
			}
			// Another instance publishes the same hash after this writer's rename.
			_, _, err := other.Put(context.Background(), strings.NewReader(content), 0)
			Expect(err).NotTo(HaveOccurred())
			return injected
		}
		_, _, err = first.Put(context.Background(), strings.NewReader(content), 0)
		Expect(err).To(MatchError(injected))
		f, err := other.Open(context.Background(), sha)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = f.Close() }()
		got, err := io.ReadAll(f)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal(content))
	})

	It("keeps a staged blob invisible until it is published", func() {
		st, err := NewFS(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		staged, err := st.(Stager).Stage(ctx, strings.NewReader("staged"), 0)
		Expect(err).NotTo(HaveOccurred())
		ok, err := st.Exists(ctx, staged.SHA())
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(staged.Publish(ctx)).To(Succeed())
		staged.Discard()
		ok, err = st.Exists(ctx, staged.SHA())
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
	})

	It("drops a staged blob that is never published", func() {
		root := GinkgoT().TempDir()
		st, err := NewFS(root)
		Expect(err).NotTo(HaveOccurred())
		sg := NewStaging(st)
		sha, _, err := sg.Put(context.Background(), strings.NewReader("abandoned"), 0)
		Expect(err).NotTo(HaveOccurred())
		sg.Close()
		ok, err := st.Exists(context.Background(), sha)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		entries, err := os.ReadDir(filepath.Join(root, "tmp"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("honors cancellation at the end of bounded ingest", func() {
		s, err := NewFS(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, _, err = s.Put(ctx, cancelAtEOF{cancel}, 128)
		Expect(err).To(MatchError(context.Canceled))
	})
})

type cancelAtEOF struct{ cancel context.CancelFunc }

func (r cancelAtEOF) Read([]byte) (int, error) { r.cancel(); return 0, io.EOF }
