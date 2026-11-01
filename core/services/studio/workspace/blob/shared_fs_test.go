// SPDX-License-Identifier: MIT
package blob_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

var _ = Describe("shared filesystem", func() {
	ctx := context.Background()
	It("does not let a materialized file mutate the authoritative blob", func() {
		s, err := blob.NewFS(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		sha, _, err := s.Put(ctx, strings.NewReader("immutable"), 0)
		Expect(err).NotTo(HaveOccurred())
		dest := GinkgoT().TempDir()
		root, err := os.OpenRoot(dest)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = root.Close() })
		Expect(s.LinkTo(ctx, sha, root, "tool-file")).To(Succeed())
		// The service UID owns the materialized inode and can chmod it. Mode 0444
		// alone cannot isolate an authoritative inode from a tool's writes.
		Expect(os.Chmod(filepath.Join(dest, "tool-file"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dest, "tool-file"), []byte("changed"), 0o600)).To(Succeed())
		r, err := s.Open(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = r.Close() }()
		got, err := io.ReadAll(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("immutable"))
	})
	It("rejects a symlinked temporary directory without writing outside the root", func() {
		dir, outside := GinkgoT().TempDir(), GinkgoT().TempDir()
		s, err := blob.NewFS(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Symlink(outside, filepath.Join(dir, "tmp"))).To(Succeed())
		_, _, err = s.Put(ctx, strings.NewReader("private upload"), 0)
		Expect(err).To(HaveOccurred())
		entries, err := os.ReadDir(outside)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})
	It("rejects symlinked shard and final paths and keeps restrictive permissions", func() {
		for _, where := range []string{"shard", "final"} {
			root, outside := GinkgoT().TempDir(), GinkgoT().TempDir()
			s, err := blob.NewFS(root)
			Expect(err).NotTo(HaveOccurred())
			sha := sum("content")
			shard := filepath.Join(root, sha[:2], sha[2:4])
			if where == "shard" {
				Expect(os.Symlink(outside, filepath.Join(root, sha[:2]))).To(Succeed())
			} else {
				Expect(os.MkdirAll(shard, 0o700)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o600)).To(Succeed())
				Expect(os.Symlink(filepath.Join(outside, "secret"), filepath.Join(shard, sha))).To(Succeed())
			}
			_, _, err = s.Put(ctx, strings.NewReader("content"), 0)
			Expect(err).To(HaveOccurred())
			_, err = s.Open(ctx, sha)
			Expect(err).To(HaveOccurred())
		}
		root := GinkgoT().TempDir()
		s, err := blob.NewFS(root)
		Expect(err).NotTo(HaveOccurred())
		sha, _, err := s.Put(ctx, strings.NewReader("content"), 0)
		Expect(err).NotTo(HaveOccurred())
		for _, path := range []string{filepath.Join(root, "tmp"), filepath.Join(root, sha[:2]), filepath.Join(root, sha[:2], sha[2:4])} {
			st, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.Mode().Perm() & 0o077).To(BeZero())
		}
		st, err := os.Stat(filepath.Join(root, sha[:2], sha[2:4], sha))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Mode().Perm() & 0o222).To(BeZero())
	})

	It("preserves default filesystem roots beneath platform directory aliases", func() {
		parent := GinkgoT().TempDir()
		real := filepath.Join(parent, "real")
		alias := filepath.Join(parent, "alias")
		Expect(os.Mkdir(real, 0o700)).To(Succeed())
		Expect(os.Symlink(real, alias)).To(Succeed())
		s, err := blob.NewFS(filepath.Join(alias, "blobs"))
		Expect(err).NotTo(HaveOccurred())
		sha, _, err := s.Put(ctx, strings.NewReader("platform alias"), 1<<20)
		Expect(err).NotTo(HaveOccurred())
		r, err := s.Open(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = r.Close() }()
		got, err := io.ReadAll(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("platform alias"))
	})

})

// Re-exec the already compiled binary; child errors propagate through Wait.
func TestSharedFSProcess(t *testing.T) {
	mode := os.Getenv("SWF_CHILD")
	if mode == "" {
		return
	}
	// This bootstrap runs without the parent Ginkgo suite in each OS process.
	g := NewWithT(t)
	s, err := blob.NewFS(os.Getenv("SWF_ROOT"))
	g.Expect(err).NotTo(HaveOccurred())
	content := os.Getenv("SWF_CONTENT")
	if mode == "read" {
		r, err := s.Open(context.Background(), sum(content))
		g.Expect(err).NotTo(HaveOccurred())
		_, err = r.Seek(1, io.SeekStart)
		g.Expect(err).NotTo(HaveOccurred())
		got, err := io.ReadAll(r)
		g.Expect(r.Close()).To(Succeed())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(got)).To(Equal(content[1:]))
		root, err := os.OpenRoot(os.Getenv("SWF_DEST"))
		g.Expect(err).NotTo(HaveOccurred())
		defer func() { _ = root.Close() }()
		g.Expect(s.LinkTo(context.Background(), sum(content), root, "file")).To(Succeed())
		return
	}
	fmt.Println("ready")
	_, err = bufio.NewReader(os.Stdin).ReadByte()
	g.Expect(err).NotTo(HaveOccurred())
	var reader io.Reader = strings.NewReader(content)
	if mode == "interrupt" {
		reader = &interruptedUpload{content: content}
	}
	_, _, err = s.Put(context.Background(), reader, 0)
	g.Expect(err).NotTo(HaveOccurred())
}

type interruptedUpload struct {
	content string
	sent    bool
}

func (r *interruptedUpload) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.content[:len(r.content)/2]), nil
	}
	fmt.Println("partial")
	_, err := bufio.NewReader(os.Stdin).ReadByte()
	return 0, err
}

var _ = Describe("shared filesystem independent processes", func() {
	It("publishes concurrent identical and distinct contents and reads after restart", func() {
		root := GinkgoT().TempDir()
		type child struct {
			cmd *exec.Cmd
			in  io.WriteCloser
		}
		var children []child
		for _, content := range []string{"identical bytes", "identical bytes", "distinct bytes"} {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSharedFSProcess$")
			cmd.Env = append(os.Environ(), "SWF_CHILD=put", "SWF_ROOT="+root, "SWF_CONTENT="+content)
			in, err := cmd.StdinPipe()
			Expect(err).NotTo(HaveOccurred())
			out, err := cmd.StdoutPipe()
			Expect(err).NotTo(HaveOccurred())
			cmd.Stderr = GinkgoWriter
			Expect(cmd.Start()).To(Succeed())
			line, err := bufio.NewReader(out).ReadString('\n')
			Expect(err).NotTo(HaveOccurred())
			Expect(line).To(Equal("ready\n"))
			children = append(children, child{cmd, in})
		}
		for _, c := range children {
			_, err := c.in.Write([]byte("x"))
			Expect(err).NotTo(HaveOccurred())
			Expect(c.in.Close()).To(Succeed())
		}
		for _, c := range children {
			Expect(c.cmd.Wait()).To(Succeed())
		}
		for _, content := range []string{"identical bytes", "distinct bytes"} {
			dest := GinkgoT().TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestSharedFSProcess$")
			cmd.Env = append(os.Environ(), "SWF_CHILD=read", "SWF_ROOT="+root, "SWF_CONTENT="+content, "SWF_DEST="+dest)
			out, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out))
			got, err := os.ReadFile(filepath.Join(dest, "file"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal(content))
		}
	})
	It("never publishes a partial hash when an uploader dies after partial ingest", func() {
		root := GinkgoT().TempDir()
		content := strings.Repeat("incomplete", 1000)
		cmd := exec.Command(os.Args[0], "-test.run=^TestSharedFSProcess$")
		cmd.Env = append(os.Environ(), "SWF_CHILD=interrupt", "SWF_ROOT="+root, "SWF_CONTENT="+content)
		in, err := cmd.StdinPipe()
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = in.Close() }()
		out, err := cmd.StdoutPipe()
		Expect(err).NotTo(HaveOccurred())
		Expect(cmd.Start()).To(Succeed())
		lines := bufio.NewReader(out)
		line, err := lines.ReadString('\n')
		Expect(err).NotTo(HaveOccurred())
		Expect(line).To(Equal("ready\n"))
		_, err = in.Write([]byte("x"))
		Expect(err).NotTo(HaveOccurred())
		line, err = lines.ReadString('\n')
		Expect(err).NotTo(HaveOccurred())
		Expect(line).To(Equal("partial\n"))
		Expect(cmd.Process.Kill()).To(Succeed())
		Expect(cmd.Wait()).NotTo(Succeed())
		s, err := blob.NewFS(root)
		Expect(err).NotTo(HaveOccurred())
		exists, err := s.Exists(context.Background(), sum(content))
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
		files, err := os.ReadDir(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(HaveLen(1))
		Expect(files[0].Name()).To(Equal("tmp"))
	})
})
