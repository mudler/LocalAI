// SPDX-License-Identifier: MIT
// Package packages stages trusted distributions. Publication never activates code.
package packages

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/oci/cosignverify"
	"github.com/sigstore/sigstore-go/pkg/root"
)

type Progress struct {
	Phase string
	Bytes int64
}

// Root must be a private owner-controlled directory, never addon-writable data.
// Catalog and Host are trusted owner configuration, not request payloads.
type Installer struct {
	// SignatureTrust is optional host-provisioned trust, never catalog/artifact input.
	// Nil preserves public Sigstore TUF trust and all signature requirements.
	SignatureTrust root.TrustedMaterial
	Root           string
	Catalog        catalog.Catalog
	Host           contract.HostCompatibility
	MaxBytes       int64
	MaxFiles       int
	writeFile      func(string, os.FileMode) (fileWriter, error)
}

func (i *Installer) byteLimit() int64 {
	if i.MaxBytes > 0 {
		return i.MaxBytes
	}
	return 512 << 20
}
func (i *Installer) fileLimit() int {
	if i.MaxFiles > 0 {
		return i.MaxFiles
	}
	return 10000
}
func (i *Installer) Stage(ctx context.Context, r catalog.Release, progress func(Progress)) (out StagedPackage, err error) {
	if e := ctx.Err(); e != nil {
		return out, e
	}
	selection := r.Selection
	if selection == "" {
		selection = r.Version
	}
	trusted, e := i.Catalog.Resolve(ctx, r.SourceID, r.AddonID, selection)
	if e != nil {
		return out, e
	}
	// Do not accept a caller's substituted artifact, policy, source or digest.
	r.Selection = selection
	if r != trusted || r.Digest == "" || r.OS != i.Host.OS || r.Arch != i.Host.Arch {
		return out, ErrPackage
	}
	root, e := filepath.Abs(i.Root)
	if e != nil || i.Root == "" {
		return out, ErrPackage
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return out, e
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return out, ErrPackage
	}
	dir, e := os.MkdirTemp(root, "stage-")
	if e != nil {
		return out, e
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	if progress != nil {
		progress(Progress{Phase: "download"})
	}
	x := &extraction{seen: map[string]bool{}}
	verification := "sha256"
	if strings.HasPrefix(r.Artifact, "oci://") {
		ref, e := name.ParseReference(strings.TrimPrefix(r.Artifact, "oci://"))
		if e != nil {
			return out, ErrPackage
		}
		pinned := ref.Context().Digest(r.Digest).Name()
		transport := boundTransport{ctx: ctx, base: httpclient.New().Transport, allowLoopbackHTTP: r.AllowLoopbackHTTP}
		if r.Policy.RequireSignature {
			verifier, e := cosignverify.NewVerifier(cosignverify.Policy{Issuer: r.Policy.Issuer, Identity: r.Policy.Identity}, nil, transport)
			if i.SignatureTrust != nil {
				verifier, e = cosignverify.NewVerifierWithTrustedMaterial(cosignverify.Policy{Issuer: r.Policy.Issuer, Identity: r.Policy.Identity}, nil, transport, i.SignatureTrust)
			}
			if e != nil {
				return out, ErrPackage
			}
			if e = verifier.VerifyImage(ctx, pinned); e != nil {
				return out, ErrPackage
			}
			verification = "sha256+sigstore"
		}
		img, e := oci.GetImage(pinned, r.OS+"/"+r.Arch, nil, transport)
		if e != nil {
			return out, ErrPackage
		}
		digest, e := img.Digest()
		if e != nil || digest.String() != r.Digest {
			return out, ErrPackage
		}
		cfg, e := img.ConfigFile()
		if e != nil || cfg.OS != r.OS || cfg.Architecture != r.Arch {
			return out, ErrPackage
		}
		layers, e := img.Layers()
		if e != nil || len(layers) > i.fileLimit() {
			return out, ErrPackage
		}
		var downloaded int64
		for _, layer := range layers {
			size, e := layer.Size()
			if e != nil || size < 0 || size > i.byteLimit()-downloaded {
				return out, ErrPackage
			}
			downloaded += size
			h, e := layer.Digest()
			if e != nil || h.Algorithm != "sha256" {
				return out, ErrPackage
			}
			rc, e := layer.Compressed()
			if e != nil {
				return out, ErrPackage
			}
			f, e := i.download(ctx, rc, h.String(), root)
			rc.Close()
			if e != nil {
				return out, e
			}
			e = func() error {
				defer os.Remove(f.Name())
				defer f.Close()
				gz, e := gzip.NewReader(f)
				if e != nil {
					return e
				}
				defer gz.Close()
				return i.extract(ctx, gz, dir, x)
			}()
			if e != nil {
				return out, e
			}
		}
	} else {
		// HTTP archives have a catalog-pinned SHA256. Sigstore image signatures are
		// not archive signatures; fail closed instead of silently dropping policy.
		if r.Policy.RequireSignature {
			return out, ErrPackage
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, r.Artifact, nil)
		if e != nil {
			return out, ErrPackage
		}
		resp, e := httpclient.New().Do(req)
		if e != nil {
			return out, ErrPackage
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return out, ErrPackage
		}
		f, e := i.download(ctx, resp.Body, r.Digest, root)
		if e != nil {
			return out, e
		}
		defer os.Remove(f.Name())
		defer f.Close()
		if e = i.extract(ctx, f, dir, x); e != nil {
			return out, e
		}
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	manifestInfo, e := os.Lstat(manifestPath)
	if e != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > contract.DefaultLimits().ManifestBytes {
		return out, ErrPackage
	}
	b, e := os.ReadFile(manifestPath)
	if e != nil {
		return out, e
	}
	m, e := contract.ParseManifest(b)
	if e != nil {
		return out, e
	}
	if e = contract.ValidateManifest(m, i.Host); e != nil {
		return out, e
	}
	if m.ID != r.AddonID || m.Version != r.Version {
		return out, ErrPackage
	}
	assets := []string{m.Executable}
	if u := m.Capabilities.UI; u != nil {
		assets = append(assets, u.Entry)
		assets = append(assets, u.CSS...)
	}
	for _, asset := range assets {
		info, e := os.Lstat(filepath.Join(dir, filepath.FromSlash(asset)))
		if e != nil || !info.Mode().IsRegular() || (asset == m.Executable && info.Mode()&0100 == 0) {
			return out, ErrPackage
		}
	}
	var icon *PackageIcon
	if u := m.Capabilities.UI; u != nil && u.Icon != "" {
		if icon, e = verifyIcon(dir, u.Icon); e != nil {
			return out, e
		}
	}
	if _, e = os.Lstat(filepath.Join(dir, "receipt.json")); !os.IsNotExist(e) {
		return out, ErrPackage
	}
	if e = ctx.Err(); e != nil {
		return out, e
	}
	if progress != nil {
		progress(Progress{Phase: "verified", Bytes: x.bytes})
	}
	return StagedPackage{&stagedState{owner: i, dir: dir, receipt: PackageReceipt{Digest: r.Digest, SourceID: r.SourceID, SourceURL: r.SourceURL, Channel: r.Channel, Selection: selection, Reference: r.Artifact, Verification: verification, Manifest: m, Icon: icon}}}, nil
}

// verifyIcon opens the declared icon inside the staged package without
// following links, bounds its size and checks that its bytes have the
// signature of the declared type. An icon that cannot be served as an
// immutable image is a package error, never a warning.
func verifyIcon(dir, name string) (*PackageIcon, error) {
	media := contract.IconTypes[strings.ToLower(filepath.Ext(name))]
	if media == "" || !filepath.IsLocal(name) {
		return nil, ErrPackage
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return nil, ErrPackage
	}
	defer root.Close()
	info, e := root.Lstat(name)
	if e != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > contract.MaxIconBytes {
		return nil, ErrPackage
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, ErrPackage
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, contract.MaxIconBytes+1))
	if e != nil || int64(len(b)) != info.Size() {
		return nil, ErrPackage
	}
	signature := false
	switch media {
	case "image/png":
		signature = bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		signature = bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff})
	case "image/webp":
		signature = len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	}
	if !signature {
		return nil, ErrPackage
	}
	sum := sha256.Sum256(b)
	return &PackageIcon{Path: name, Digest: fmt.Sprintf("sha256:%x", sum), Size: int64(len(b)), MediaType: media}, nil
}
func (i *Installer) download(ctx context.Context, r io.Reader, digest, root string) (f *os.File, err error) {
	f, err = os.CreateTemp(root, "download-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(contextReader{ctx, r}, i.byteLimit()+1))
	if e != nil {
		return f, e
	}
	if n > i.byteLimit() || fmt.Sprintf("sha256:%x", h.Sum(nil)) != digest {
		return f, ErrPackage
	}
	_, err = f.Seek(0, 0)
	return f, err
}

// go-containerregistry creates its own client. Reject redirect responses at the
// transport boundary so registry credentials cannot follow to another origin.
type boundTransport struct {
	ctx               context.Context
	base              http.RoundTripper
	allowLoopbackHTTP bool
}

func (t boundTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "http" {
		ip := net.ParseIP(req.URL.Hostname())
		if !t.allowLoopbackHTTP || ip == nil || !ip.IsLoopback() {
			return nil, ErrPackage
		}
	}
	resp, e := t.base.RoundTrip(req.Clone(t.ctx))
	if e == nil && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, ErrPackage
	}
	return resp, e
}
