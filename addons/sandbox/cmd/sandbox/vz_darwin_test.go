//go:build darwin && arm64

// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/vz"
	"github.com/mudler/LocalAI/addons/sandbox/internal/tools"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// settings is what the tools need of the selector.
type selectorSettings struct {
	tools.Settings
	sel *backends.Selector
}

func (s selectorSettings) Status() backends.Status { return s.sel.Status() }

// The add-on on a Mac chooses the VM backend by itself and runs a command in
// it. The test needs a signed test binary and SANDBOX_TEST_IMAGE, the guest
// image (name@sha256:... or oci-layout:<dir>@sha256:...).
var _ = Describe("the add-on on macOS", func() {
	It("picks the macOS virtual machine with Automatic and runs a command in it", func() {
		img := os.Getenv("SANDBOX_TEST_IMAGE")
		if img == "" {
			Skip("set SANDBOX_TEST_IMAGE to the guest image")
		}
		old := guestImage
		guestImage = img
		DeferCleanup(func() { guestImage = old })
		sel, err := newSelector(GinkgoT().TempDir())
		Expect(err).To(Succeed())
		sel.Warm()
		st := sel.Status()
		Expect(st.Setting).To(Equal(backends.Auto))
		Expect(st.Active).To(Equal("vz"), "status: %+v", st)
		names := map[string]bool{}
		for _, b := range st.Backends {
			names[b.Name] = b.Usable
		}
		Expect(names).To(HaveKeyWithValue("vz", true))
		Expect(names).To(HaveKeyWithValue("kvm", false), "KVM must say why it does not run here")
		Expect(names).To(HaveKeyWithValue("gvisor", false))

		b, err := sel.Backend()
		Expect(err).To(Succeed())
		Expect(b).To(BeAssignableToTypeOf(&vz.Backend{}))
		ctx := context.Background()
		store := sbtest.OpenStore()
		_, err = store.Mkdir(ctx, sbtest.Admin, "/proj")
		Expect(err).To(Succeed())
		v, err := store.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
		Expect(err).To(Succeed())
		sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: store, Actor: sbtest.Admin}, View: v, Memory: 1 << 30, Disk: 1 << 30})
		Expect(err).To(Succeed())
		defer func() { _ = sb.Destroy(ctx) }()
		var out bytes.Buffer
		p, err := sb.Exec(ctx, sandbox.Cmd{Args: []string{"sh", "-c", "uname -m; id -u; echo hello > proj/hello.txt"}, Stdout: &out})
		Expect(err).To(Succeed())
		code, err := p.Wait()
		Expect(err).To(Succeed())
		Expect(code).To(BeZero())
		Expect(out.String()).To(Equal("aarch64\n1000\n"))
		cs, _, err := sb.Checkpoint(ctx, "hello")
		Expect(err).To(Succeed())
		Expect(cs.Rev).NotTo(BeZero())

		// The status tool tells the host that the commands are isolated and that
		// the network policy is enforced, which the document skills and the
		// network setting of the chats depend on.
		rt := tools.New(nil, selectorSettings{sel: sel}, nil)
		var env contract.Envelope
		env.Request.OperationID = "typed-read"
		env.Request.Arguments = json.RawMessage(`{"tool":"execution-status"}`)
		res, err := rt.Read(ctx, env)
		Expect(err).To(Succeed())
		status, err := contract.DecodeExecutionStatus(res.Payload)
		Expect(err).To(Succeed())
		Expect(status.Readiness).To(Equal("available"))
		Expect(status.Isolation).To(Equal("isolated"))
		Expect(status.NetworkPolicyEnforced).To(BeTrue())
		Expect(strings.TrimSpace(status.Explanation)).To(BeEmpty())
	})

	It("does not replace an explicit choice that does not work", func() {
		sel, err := newSelector(GinkgoT().TempDir())
		Expect(err).To(Succeed())
		Expect(sel.Set(backends.KVM)).To(Succeed())
		_, err = sel.Backend()
		Expect(err).To(HaveOccurred(), "kvm was chosen on a Mac, but a backend ran")
		st := sel.Status()
		Expect(st.Active).To(BeEmpty())
		for _, b := range st.Backends {
			if b.Name == "kvm" {
				Expect(b.Reason).To(ContainSubstring("KVM exists only on Linux"))
			}
		}
	})
})
