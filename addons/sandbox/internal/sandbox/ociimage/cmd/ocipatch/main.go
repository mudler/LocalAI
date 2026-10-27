// Command ocipatch copies an OCI layout and adds one layer on top of its
// image that holds the given files. It is a development tool: it puts a
// freshly built guest agent into a guest image without a container engine.
//
//	ocipatch -in guest-oci -out guest-oci-new -add usr/sbin/sandbox-agent=./sandbox-agent
//
// It prints the reference of the new image: oci-layout:<out>@sha256:<hex>.
package main

import (
	"archive/tar"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

type addList []string

func (a *addList) String() string     { return strings.Join(*a, ",") }
func (a *addList) Set(v string) error { *a = append(*a, v); return nil }

func main() {
	in := flag.String("in", "", "OCI layout folder to read (its first image)")
	out := flag.String("out", "", "OCI layout folder to create")
	var add addList
	flag.Var(&add, "add", "image/path=host/file, repeatable")
	flag.Parse()
	if *in == "" || *out == "" || len(add) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	ref, err := patch(*in, *out, add)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ocipatch:", err)
		os.Exit(1)
	}
	fmt.Println(ref)
}

func patch(in, out string, add []string) (string, error) {
	// layout.Write would reuse an existing layout, and AppendImage would add a
	// second manifest to it instead of failing.
	if absIn, absOut := abs(in), abs(out); absIn == absOut {
		return "", fmt.Errorf("-out %s is the same as -in", out)
	}
	if _, err := os.Lstat(out); err == nil {
		return "", fmt.Errorf("-out %s exists: give a folder that does not exist", out)
	}
	idx, err := layout.ImageIndexFromPath(in)
	if err != nil {
		return "", err
	}
	m, err := idx.IndexManifest()
	if err != nil {
		return "", err
	}
	if len(m.Manifests) == 0 {
		return "", fmt.Errorf("%s has no image", in)
	}
	src := m.Manifests[0]
	img, err := idx.Image(src.Digest)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, a := range add {
		dst, file, ok := strings.Cut(a, "=")
		if !ok || dst == "" || file == "" || strings.HasPrefix(dst, "/") {
			return "", fmt.Errorf("-add %q: want image/path=host/file", a)
		}
		if path.Clean(dst) != dst || dst == ".." || strings.HasPrefix(dst, "../") {
			return "", fmt.Errorf("-add %q: the image path must be clean and must not leave the image root", a)
		}
		body, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		if err := tw.WriteHeader(&tar.Header{Name: dst, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
			return "", err
		}
		if _, err := tw.Write(body); err != nil {
			return "", err
		}
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf.Bytes())), nil })
	if err != nil {
		return "", err
	}
	patched, err := mutate.AppendLayers(img, layer)
	if err != nil {
		return "", err
	}
	// Keep the platform of the source descriptor; add none when it has none.
	var opts []layout.Option
	if src.Platform != nil {
		opts = append(opts, layout.WithPlatform(*src.Platform))
	}
	p, err := layout.Write(out, empty.Index)
	if err != nil {
		return "", err
	}
	if err := p.AppendImage(patched, opts...); err != nil {
		return "", err
	}
	d, err := patched.Digest()
	if err != nil {
		return "", err
	}
	return "oci-layout:" + out + "@" + d.String(), nil
}

// abs returns the absolute path of p, or p when that fails.
func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
