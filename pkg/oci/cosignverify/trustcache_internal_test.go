// SPDX-License-Identifier: MIT
package cosignverify

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

var _ = Describe("loading the Sigstore trusted root", func() {
	var calls []tuf.Options

	// fake answers from the queue, one result per call, and records the
	// options of each call.
	fake := func(results ...error) {
		calls = nil
		previous := fetchTrustedRoot
		DeferCleanup(func() { fetchTrustedRoot = previous })
		fetchTrustedRoot = func(opts *tuf.Options) (*root.TrustedRoot, error) {
			calls = append(calls, *opts)
			err := results[0]
			results = results[1:]
			if err != nil {
				return nil, err
			}
			return &root.TrustedRoot{}, nil
		}
	}
	verifier := func(p Policy) *Verifier {
		p.Issuer, p.Identity = "https://issuer.invalid", "builder@example.invalid"
		v, err := NewVerifier(p, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		return v
	}

	It("reads the root without a disk cache when the default cache cannot be made", func() {
		// The regression of a container whose home the LocalAI user cannot
		// write: every signed add-on install failed.
		fake(errors.New("mkdir /home/localai/.sigstore: permission denied"), nil)
		v := verifier(Policy{TUFRootURL: "https://tuf.invalid/no-cache"})
		material, err := v.loadTrustedMaterial()
		Expect(err).NotTo(HaveOccurred())
		Expect(material).To(HaveLen(1))
		Expect(calls).To(HaveLen(2))
		Expect(calls[0].DisableLocalCache).To(BeFalse())
		Expect(calls[1].DisableLocalCache).To(BeTrue())
		Expect(calls[1].RepositoryBaseURL).To(Equal("https://tuf.invalid/no-cache"))
	})

	It("does not keep a failure, so the next verification tries again", func() {
		down := errors.New("tuf mirror unreachable")
		fake(down, down, nil)
		v := verifier(Policy{TUFRootURL: "https://tuf.invalid/retry"})
		_, err := v.loadTrustedMaterial()
		Expect(err).To(MatchError(down))
		material, err := v.loadTrustedMaterial()
		Expect(err).NotTo(HaveOccurred())
		Expect(material).To(HaveLen(1))
		// The root that loaded is kept: no further fetch.
		_, err = v.loadTrustedMaterial()
		Expect(err).NotTo(HaveOccurred())
		Expect(calls).To(HaveLen(3))
	})

	It("keeps the error of a writable cache path the caller chose", func() {
		denied := errors.New("tuf mirror refused the metadata")
		fake(denied)
		chosen := GinkgoT().TempDir()
		v := verifier(Policy{TUFRootURL: "https://tuf.invalid/explicit", TUFCachePath: chosen})
		_, err := v.loadTrustedMaterial()
		Expect(err).To(MatchError(denied))
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].CachePath).To(Equal(chosen))
	})

	It("reads the root without a disk cache when the chosen cache path is not writable", func() {
		// A data volume that the LocalAI user cannot write must not refuse
		// every signed install.
		parent := GinkgoT().TempDir()
		Expect(os.Chmod(parent, 0o500)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(parent, 0o700) })
		chosen := filepath.Join(parent, "sigstore")
		if dirWritable(chosen) {
			Skip("the test runs as a user that can write a read-only folder (root)")
		}
		fake(errors.New("mkdir: permission denied"), nil)
		v := verifier(Policy{TUFRootURL: "https://tuf.invalid/unwritable", TUFCachePath: chosen})
		material, err := v.loadTrustedMaterial()
		Expect(err).NotTo(HaveOccurred())
		Expect(material).To(HaveLen(1))
		Expect(calls).To(HaveLen(2))
		Expect(calls[0].CachePath).To(Equal(chosen))
		Expect(calls[1].DisableLocalCache).To(BeTrue())
	})

	It("uses the process default cache path when the policy sets none", func() {
		dir := GinkgoT().TempDir()
		SetDefaultTUFCachePath(dir)
		DeferCleanup(func() { SetDefaultTUFCachePath("") })
		fake(nil)
		v := verifier(Policy{TUFRootURL: "https://tuf.invalid/default-path"})
		_, err := v.loadTrustedMaterial()
		Expect(err).NotTo(HaveOccurred())
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].CachePath).To(Equal(dir))
		Expect(calls[0].DisableLocalCache).To(BeFalse())
	})
})
