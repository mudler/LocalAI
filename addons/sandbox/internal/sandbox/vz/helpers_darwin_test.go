//go:build darwin && arm64

package vz

import (
	"archive/tar"
	"bytes"
	"io"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

// attrLayer is a layer with every file kind the conversion must keep: a
// setuid file, a symlink, a hard link, a fifo, a character device and a file
// with an xattr.
func attrLayer() v1.Layer {
	GinkgoHelper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		Expect(tw.WriteHeader(h)).To(Succeed())
		_, err := io.WriteString(tw, body)
		Expect(err).To(Succeed())
	}
	add(&tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "opt/attrs/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "opt/attrs/suid", Typeflag: tar.TypeReg, Mode: 0o4755}, "#!/bin/sh\n")
	add(&tar.Header{Name: "opt/attrs/link", Typeflag: tar.TypeSymlink, Linkname: "suid", Mode: 0o777}, "")
	add(&tar.Header{Name: "opt/attrs/hard", Typeflag: tar.TypeLink, Linkname: "opt/attrs/suid"}, "")
	add(&tar.Header{Name: "opt/attrs/fifo", Typeflag: tar.TypeFifo, Mode: 0o600, Uid: 1000, Gid: 1000}, "")
	add(&tar.Header{Name: "opt/attrs/null", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 1, Devminor: 3}, "")
	add(&tar.Header{Name: "opt/attrs/xattr", Typeflag: tar.TypeReg, Mode: 0o644, PAXRecords: map[string]string{"SCHILY.xattr.user.demo": "hello"}}, "x\n")
	Expect(tw.Close()).To(Succeed())
	b := buf.Bytes()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil })
	Expect(err).To(Succeed())
	return l
}

// writeLayout stores img in an OCI layout folder and returns its reference.
func writeLayout(img v1.Image) string {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	p, err := layout.Write(dir, empty.Index)
	Expect(err).To(Succeed())
	Expect(p.AppendImage(img)).To(Succeed())
	d, err := img.Digest()
	Expect(err).To(Succeed())
	return ociimage.LayoutPrefix + dir + "@" + d.String()
}

// agentLayer is a layer that replaces the guest agent with a script. The
// script runs as PID 1, which is root, so it can power the VM off the way
// the agent does after a failed boot step.
func agentLayer(script string) func() (io.ReadCloser, error) {
	GinkgoHelper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	Expect(tw.WriteHeader(&tar.Header{Name: "usr/sbin/sandbox-agent", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(script))})).To(Succeed())
	_, err := io.WriteString(tw, script)
	Expect(err).To(Succeed())
	Expect(tw.Close()).To(Succeed())
	b := buf.Bytes()
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
}
