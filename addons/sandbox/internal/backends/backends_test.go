// SPDX-License-Identifier: MIT
package backends_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stub is a backend that only says its name.
type stub struct {
	name   string
	closed atomic.Bool
}

func (s *stub) Create(context.Context, sandbox.Spec) (sandbox.Sandbox, error) {
	return nil, errors.New(s.name)
}

func (s *stub) Resume(context.Context, sandbox.SnapshotRef, sandbox.Env) (sandbox.Sandbox, error) {
	return nil, errors.New(s.name)
}
func (s *stub) Close() error { s.closed.Store(true); return nil }

var _ = workspace.ErrInvalid

// kinds says which backends can start on the fake host.
type kinds struct{ kvm, gvisor, host error }

func TestBackends(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox Backends Suite")
}

func open(k kinds, dir string) *backends.Selector {
	mk := func(name string, err error) func() (sandbox.Backend, error) {
		return func() (sandbox.Backend, error) {
			if err != nil {
				return nil, err
			}
			return &stub{name: name}, nil
		}
	}
	s, err := backends.Open(backends.Options{
		DataDir: dir,
		KVM:     mk("kvm", k.kvm), GVisor: mk("gvisor", k.gvisor), Host: mk("host", k.host),
	})
	Expect(err).NotTo(HaveOccurred())
	return s
}

func name(s *backends.Selector) string {
	b, err := s.Backend()
	if err != nil {
		if errors.Is(err, runner.ErrNoSandbox) {
			return ""
		}
		Expect(err).NotTo(HaveOccurred())
	}
	return b.(*stub).name
}

var unsupported = fmt.Errorf("%w: /dev/kvm: permission denied", sandbox.ErrUnsupported)

var _ = Describe("Backend selector", func() {
	It("picks KVM, then gVisor, then nothing, and never the host", func() {
		cases := []struct {
			k    kinds
			want string
		}{
			{kinds{}, "kvm"},
			{kinds{kvm: unsupported}, "gvisor"},
			{kinds{kvm: unsupported, gvisor: unsupported}, ""},
		}
		for _, c := range cases {
			s := open(c.k, GinkgoT().TempDir())
			Expect(name(s)).To(Equal(c.want), "%+v", c.k)
		}
	})

	It("never picks the host automatically, even when it was confirmed", func() {
		s := open(kinds{kvm: unsupported, gvisor: unsupported}, GinkgoT().TempDir())
		Expect(s.ConfirmHost(backends.HostPhrase)).To(Succeed())
		Expect(name(s)).To(BeEmpty(), "auto picked a backend after the host was confirmed")
		st := s.Status()
		Expect(st.Active).To(BeEmpty())
		Expect(st.Setting).To(Equal(backends.Auto))
	})

	It("never treats an explicit choice as a fallback", func() {
		s := open(kinds{kvm: unsupported}, GinkgoT().TempDir())
		Expect(s.Set(backends.KVM)).To(Succeed())
		Expect(name(s)).To(BeEmpty(), "kvm was chosen but another backend ran")
		Expect(s.Set(backends.GVisor)).To(Succeed())
		Expect(name(s)).To(Equal("gvisor"), "gvisor was chosen")
		Expect(s.Set(backends.Off)).To(Succeed())
		Expect(name(s)).To(BeEmpty(), "off, but a backend ran")
	})

	It("needs the typed phrase before the host can be chosen", func() {
		s := open(kinds{}, GinkgoT().TempDir())
		Expect(s.Set(backends.Host)).To(MatchError(backends.ErrNotConfirmed), "Set(host) before the phrase")
		for _, bad := range []string{"", "yes", "i understand", strings.ToLower(backends.HostPhrase), backends.HostPhrase + " "} {
			Expect(s.ConfirmHost(bad)).To(MatchError(backends.ErrNotConfirmed), "ConfirmHost(%q)", bad)
		}
		Expect(s.ConfirmHost(backends.HostPhrase)).To(Succeed())
		Expect(s.Set(backends.Host)).To(Succeed())
		Expect(name(s)).To(Equal("host"))
		st := s.Status()
		Expect(st.HostNotice).To(BeTrue(), "want the persistent notice: %+v", st)
		Expect(st.Active).To(Equal("host"))
	})

	It("refuses the host where it cannot run", func() {
		s := open(kinds{kvm: unsupported, gvisor: unsupported, host: fmt.Errorf("%w: the host backend must not run as root", sandbox.ErrUnsupported)}, GinkgoT().TempDir())
		_ = s.ConfirmHost(backends.HostPhrase)
		Expect(s.Set(backends.Host)).To(MatchError(ContainSubstring("must not run as root")))
		Expect(name(s)).To(BeEmpty())
	})

	It("says why each backend is missing", func() {
		s := open(kinds{kvm: unsupported, gvisor: fmt.Errorf("%w: runsc not found", sandbox.ErrUnsupported)}, GinkgoT().TempDir())
		st := s.Status()
		reasons := map[string]string{}
		for _, b := range st.Backends {
			reasons[b.Name] = b.Reason
			if b.Name == "host" {
				Expect(b.Usable).To(BeTrue(), "the host backend should be usable here: %+v", b)
			}
		}
		Expect(reasons["kvm"]).To(ContainSubstring("/dev/kvm"))
		Expect(reasons["gvisor"]).To(ContainSubstring("runsc"))
		Expect(reasons["kvm"]).NotTo(ContainSubstring("unsupported"), "the reason repeats the error class")
	})

	It("keeps the choice across a restart and falls back to auto on a broken file", func() {
		dir := GinkgoT().TempDir()
		s := open(kinds{}, dir)
		Expect(s.Set(backends.GVisor)).To(Succeed())
		Expect(open(kinds{}, dir).Status().Setting).To(Equal(backends.GVisor), "setting after restart")
		info, err := os.Stat(filepath.Join(dir, "settings.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		Expect(os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{broken"), 0o600)).To(Succeed())
		Expect(open(kinds{}, dir).Status().Setting).To(Equal(backends.Auto), "a broken file")
		// A setting nobody offers is not accepted from the file either, and the
		// host is not selected by a file that says so without a confirmation.
		Expect(os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"backend":"host"}`), 0o600)).To(Succeed())
		Expect(open(kinds{}, dir).Status().Setting).NotTo(Equal(backends.Host), "the file selected the host without a confirmation")
		Expect(s.Set("anything")).To(MatchError(backends.ErrUnknown))
	})

	It("closes the backend that was running when it switches", func() {
		s := open(kinds{}, GinkgoT().TempDir())
		b, err := s.Backend()
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Set(backends.Off)).To(Succeed())
		Expect(b.(*stub).closed.Load()).To(BeTrue(), "the backend was not closed when the choice changed")
	})

	It("names the exact admin step in the AppArmor reason", func() {
		dir := GinkgoT().TempDir()
		runsc := filepath.Join(dir, "runsc")
		tool := filepath.Join(dir, "gvisor-profile")
		Expect(os.WriteFile(tool, []byte("x"), 0o755)).To(Succeed())
		err := fmt.Errorf("%w: runsc cannot make a user namespace (permission denied): an administrator must load the AppArmor profile that gvisor.Profile(%q) returns", sandbox.ErrUnsupported, runsc)
		s := open(kinds{kvm: unsupported, gvisor: err}, GinkgoT().TempDir())
		var got string
		for _, b := range s.Status().Backends {
			if b.Name == "gvisor" {
				got = b.Reason
			}
		}
		for _, want := range []string{tool + " " + runsc, "/etc/apparmor.d/", "apparmor_parser -r", "Ubuntu 24.04"} {
			Expect(got).To(ContainSubstring(want))
		}
		Expect(got).NotTo(ContainSubstring("gvisor.Profile"), "reason names a Go function")
	})

	It("names the command in the AppArmor reason without the shipped tool", func() {
		err := fmt.Errorf("%w: runsc cannot make a user namespace (permission denied): an administrator must load the AppArmor profile that gvisor.Profile(%q) returns", sandbox.ErrUnsupported, "/opt/gvisor/runsc")
		s := open(kinds{kvm: unsupported, gvisor: err}, GinkgoT().TempDir())
		var got string
		for _, b := range s.Status().Backends {
			if b.Name == "gvisor" {
				got = b.Reason
			}
		}
		Expect(got).To(ContainSubstring("gvisor-profile /opt/gvisor/runsc"))
		Expect(got).To(ContainSubstring("apparmor_parser -r"))
	})

	It("says how to run unprivileged when it refuses root", func() {
		err := fmt.Errorf("%w: the backend must not run as root on the host: run it as an unprivileged user", sandbox.ErrUnsupported)
		s := open(kinds{kvm: err, gvisor: err}, GinkgoT().TempDir())
		for _, b := range s.Status().Backends {
			if b.Name == "kvm" {
				Expect(b.Reason).To(ContainSubstring("LOCALAI_RUN_AS_ROOT"))
				Expect(b.Reason).To(ContainSubstring("unprivileged"))
			}
		}
	})

	// A backend sweeps the sandboxes an earlier process left when it starts. It
	// starts at the first call, so after a restart nothing was swept until
	// someone used the sandbox. Warm starts the chosen backend at once.
	It("starts the chosen backend on Warm without a call", func() {
		var started atomic.Int32
		mk := func(name string) func() (sandbox.Backend, error) {
			return func() (sandbox.Backend, error) { started.Add(1); return &stub{name: name}, nil }
		}
		s, err := backends.Open(backends.Options{DataDir: GinkgoT().TempDir(), KVM: mk("kvm"), GVisor: mk("gvisor"), Host: mk("host")})
		Expect(err).NotTo(HaveOccurred())
		Expect(started.Load()).To(BeZero(), "Open started a backend")
		s.Warm()
		Expect(started.Load()).To(BeEquivalentTo(1), "Warm must start the 1 backend that auto picks")
		Expect(name(s)).To(Equal("kvm"))
		Expect(started.Load()).To(BeEquivalentTo(1), "the backend must be reused after Warm")
	})

	It("is quiet when Warm has no usable backend", func() {
		s := open(kinds{kvm: unsupported, gvisor: unsupported}, GinkgoT().TempDir())
		s.Warm() // must not panic or block
		Expect(name(s)).To(BeEmpty())
	})
})
