// SPDX-License-Identifier: MIT
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("a batch of staged blobs", func() {
	ctx := context.Background()

	newBatch := func() (*fsStore, string) {
		root := GinkgoT().TempDir()
		st, err := NewFS(root)
		Expect(err).NotTo(HaveOccurred())
		return st.(*fsStore), root
	}
	body := func(i int) string { return fmt.Sprintf("blob number %d", i) }

	It("is invisible until Publish, then complete", func() {
		s, root := newBatch()
		sg := NewStaging(s)
		defer sg.Close()
		var shas []string
		for i := range 3*syncChunk + 5 { // more than one chunk of syncs
			sha, _, err := sg.Put(ctx, strings.NewReader(body(i)), 0)
			Expect(err).NotTo(HaveOccurred())
			shas = append(shas, sha)
		}
		Expect(sg.Flush()).To(Succeed())
		for _, sha := range shas {
			ok, err := s.Exists(ctx, sha)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse(), "visible before Publish")
		}
		Expect(sg.Publish(ctx)).To(Succeed())
		for i, sha := range shas {
			f, err := s.Open(ctx, sha)
			Expect(err).NotTo(HaveOccurred())
			got, err := io.ReadAll(f)
			_ = f.Close()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal(body(i)))
		}
		sg.Close()
		entries, err := os.ReadDir(filepath.Join(root, "tmp"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("syncs each file once and each folder once", func() {
		s, root := newBatch()
		var files, dirs atomic.Int64
		s.fault = func(at string) error {
			switch at {
			case "file sync":
				files.Add(1)
			case "directory sync":
				dirs.Add(1)
			}
			return nil
		}
		sg := NewStaging(s)
		defer sg.Close()
		const n = 200
		leaves, mids := map[string]bool{}, map[string]bool{}
		for i := range n {
			sha, _, err := sg.Put(ctx, strings.NewReader(body(i)), 0)
			Expect(err).NotTo(HaveOccurred())
			leaf := filepath.Dir(shaPath(root, sha))
			leaves[leaf], mids[filepath.Dir(leaf)] = true, true
		}
		Expect(sg.Publish(ctx)).To(Succeed())
		Expect(files.Load()).To(BeEquivalentTo(n))
		// The folders of the batch: the leaf folders, their parents, the root,
		// and tmp (made by the first stage). Not two or more for each blob.
		Expect(dirs.Load()).To(BeNumerically("<=", len(leaves)+len(mids)+2))
	})

	It("a failed file sync fails the batch and leaves nothing", func() {
		s, root := newBatch()
		injected := errors.New("injected file sync")
		var calls atomic.Int64
		s.fault = func(at string) error {
			if at == "file sync" && calls.Add(1) == 70 { // in the second chunk
				return injected
			}
			return nil
		}
		sg := NewStaging(s)
		var shas []string
		var err error
		for i := range 2 * syncChunk {
			var sha string
			if sha, _, err = sg.Put(ctx, strings.NewReader(body(i)), 0); err != nil {
				break
			}
			shas = append(shas, sha)
		}
		if err == nil {
			err = sg.Publish(ctx)
		}
		Expect(err).To(MatchError(injected))
		sg.Close()
		for _, sha := range shas {
			ok, err := s.Exists(ctx, sha)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse(), "a blob of a failed batch is visible")
		}
		entries, err := os.ReadDir(filepath.Join(root, "tmp"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("a failed folder sync fails Publish and leaves no temporary file", func() {
		s, root := newBatch()
		sg := NewStaging(s)
		for i := range 10 {
			_, _, err := sg.Put(ctx, strings.NewReader(body(i)), 0)
			Expect(err).NotTo(HaveOccurred())
		}
		injected := errors.New("injected directory sync")
		s.fault = func(at string) error {
			if at == "directory sync" {
				return injected
			}
			return nil
		}
		Expect(sg.Publish(ctx)).To(MatchError(injected))
		sg.Close()
		entries, err := os.ReadDir(filepath.Join(root, "tmp"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("takes the same bytes twice", func() {
		s, _ := newBatch()
		sg := NewStaging(s)
		defer sg.Close()
		a, _, err := sg.Put(ctx, strings.NewReader("same"), 0)
		Expect(err).NotTo(HaveOccurred())
		b, _, err := sg.Put(ctx, strings.NewReader("same"), 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(b))
		Expect(sg.Publish(ctx)).To(Succeed())
		ok, err := s.Exists(ctx, a)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
	})

	It("is safe when two batches run at once", func() {
		s, _ := newBatch()
		var wg sync.WaitGroup
		for g := range 4 {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				sg := NewStaging(s)
				defer sg.Close()
				for i := range 80 {
					_, _, err := sg.Put(ctx, strings.NewReader(body(i+g*40)), 0) // the batches overlap
					Expect(err).NotTo(HaveOccurred())
				}
				Expect(sg.Publish(ctx)).To(Succeed())
			}()
		}
		wg.Wait()
	})
})
