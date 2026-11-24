//go:build linux

// SPDX-License-Identifier: MIT
package addons

import (
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("trusted identity of an installed add-on", func() {
	It("follows install and uninstall without the manager lock", func() {
		r := newUpdateRig(false)
		Expect(r.m.TrustedIdentity("sample")).To(BeEmpty())
		r.lib.transform = nil
		rel := r.lib.publish("1.2.3-dev", "development", "a", catalog.Requirements{})
		r.lib.refresh()
		_, e := r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "i1", AddonID: "sample", Action: "install", SourceID: "test", Version: "development"})
		Expect(e).NotTo(HaveOccurred(), rel.Digest)
		Expect(r.m.TrustedIdentity("sample")).To(Equal("disk-one:sample"))

		// The Authority calls it from inside admission, which holds the lock.
		r.m.mu.Lock()
		got := r.m.TrustedIdentity("sample")
		r.m.mu.Unlock()
		Expect(got).To(Equal("disk-one:sample"))

		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "x1", AddonID: "sample", Action: "uninstall"})
		Expect(e).NotTo(HaveOccurred())
		Expect(r.m.TrustedIdentity("sample")).To(BeEmpty())
	})
})
