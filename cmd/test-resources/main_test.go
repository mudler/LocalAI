// SPDX-License-Identifier: MIT

package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/onsi/gomega"
)

func TestNormalizeDockerArchiveIgnoresTarMetadata(t *testing.T) {
	g := gomega.NewWithT(t)
	first := dockerArchive(g, time.Unix(100, 0), 12, "builder")
	second := dockerArchive(g, time.Unix(200, 0), 34, "runner")

	var normalizedFirst, normalizedSecond bytes.Buffer
	g.Expect(normalizeDockerArchive(bytes.NewReader(first), &normalizedFirst)).To(gomega.Succeed())
	g.Expect(normalizeDockerArchive(bytes.NewReader(second), &normalizedSecond)).To(gomega.Succeed())
	firstDigest := fmt.Sprintf("%x", sha256.Sum256(normalizedFirst.Bytes()))
	secondDigest := fmt.Sprintf("%x", sha256.Sum256(normalizedSecond.Bytes()))
	g.Expect(secondDigest).To(gomega.Equal(firstDigest))

	tr := tar.NewReader(bytes.NewReader(normalizedFirst.Bytes()))
	header, err := tr.Next()
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(header.Uid).To(gomega.Equal(0))
	g.Expect(header.Gid).To(gomega.Equal(0))
	g.Expect(header.Uname).To(gomega.BeEmpty())
	g.Expect(header.Gname).To(gomega.BeEmpty())
	g.Expect(header.ModTime).To(gomega.Equal(time.Unix(0, 0)))
	content, err := io.ReadAll(tr)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(string(content)).To(gomega.Equal("image data"))
}

func dockerArchive(g *gomega.WithT, modTime time.Time, uid int, user string) []byte {
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	content := []byte("image data")
	g.Expect(tw.WriteHeader(&tar.Header{
		Name: "layer.tar", Mode: 0o644, Size: int64(len(content)),
		ModTime: modTime, Uid: uid, Gid: uid, Uname: user, Gname: user,
	})).To(gomega.Succeed())
	_, err := tw.Write(content)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(tw.Close()).To(gomega.Succeed())
	return archive.Bytes()
}
