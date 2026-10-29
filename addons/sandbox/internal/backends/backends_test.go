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
	"github.com/mudler/LocalAI/addons/sandbox/internal/codetools"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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

func open(t *testing.T, k kinds, dir string) *backends.Selector {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func name(t *testing.T, s *backends.Selector) string {
	t.Helper()
	b, err := s.Backend()
	if err != nil {
		if errors.Is(err, codetools.ErrNoSandbox) {
			return ""
		}
		t.Fatal(err)
	}
	return b.(*stub).name
}

var unsupported = fmt.Errorf("%w: /dev/kvm: permission denied", sandbox.ErrUnsupported)

func TestAutoPicksKVMThenGVisorThenNothingAndNeverTheHost(t *testing.T) {
	cases := []struct {
		k    kinds
		want string
	}{
		{kinds{}, "kvm"},
		{kinds{kvm: unsupported}, "gvisor"},
		{kinds{kvm: unsupported, gvisor: unsupported}, ""},
	}
	for _, c := range cases {
		s := open(t, c.k, t.TempDir())
		if got := name(t, s); got != c.want {
			t.Errorf("%+v: auto picked %q, want %q", c.k, got, c.want)
		}
	}
}

func TestAutoNeverPicksTheHostEvenWhenItWasConfirmed(t *testing.T) {
	s := open(t, kinds{kvm: unsupported, gvisor: unsupported}, t.TempDir())
	if err := s.ConfirmHost(backends.HostPhrase); err != nil {
		t.Fatal(err)
	}
	if got := name(t, s); got != "" {
		t.Fatalf("auto picked %q after the host was confirmed", got)
	}
	if st := s.Status(); st.Active != "" || st.Setting != backends.Auto {
		t.Fatalf("status = %+v", st)
	}
}

func TestAnExplicitChoiceIsNeverAFallback(t *testing.T) {
	s := open(t, kinds{kvm: unsupported}, t.TempDir())
	if err := s.Set(backends.KVM); err != nil {
		t.Fatal(err)
	}
	if got := name(t, s); got != "" {
		t.Fatalf("kvm was chosen but %q ran", got)
	}
	if err := s.Set(backends.GVisor); err != nil {
		t.Fatal(err)
	}
	if got := name(t, s); got != "gvisor" {
		t.Fatalf("gvisor was chosen but %q ran", got)
	}
	if err := s.Set(backends.Off); err != nil {
		t.Fatal(err)
	}
	if got := name(t, s); got != "" {
		t.Fatalf("off, but %q ran", got)
	}
}

func TestTheHostNeedsTheTypedPhraseBeforeItCanBeChosen(t *testing.T) {
	s := open(t, kinds{}, t.TempDir())
	if err := s.Set(backends.Host); !errors.Is(err, backends.ErrNotConfirmed) {
		t.Fatalf("Set(host) before the phrase = %v, want ErrNotConfirmed", err)
	}
	for _, bad := range []string{"", "yes", "i understand", strings.ToLower(backends.HostPhrase), backends.HostPhrase + " "} {
		if err := s.ConfirmHost(bad); !errors.Is(err, backends.ErrNotConfirmed) {
			t.Errorf("ConfirmHost(%q) = %v, want ErrNotConfirmed", bad, err)
		}
	}
	if err := s.ConfirmHost(backends.HostPhrase); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(backends.Host); err != nil {
		t.Fatal(err)
	}
	if got := name(t, s); got != "host" {
		t.Fatalf("host chosen, %q ran", got)
	}
	st := s.Status()
	if !st.HostNotice || st.Active != "host" {
		t.Fatalf("status = %+v, want the persistent notice", st)
	}
}

func TestTheHostRefusesWhereItCannotRun(t *testing.T) {
	s := open(t, kinds{kvm: unsupported, gvisor: unsupported, host: fmt.Errorf("%w: the host backend must not run as root", sandbox.ErrUnsupported)}, t.TempDir())
	_ = s.ConfirmHost(backends.HostPhrase)
	err := s.Set(backends.Host)
	if err == nil || !strings.Contains(err.Error(), "must not run as root") {
		t.Fatalf("Set(host) = %v, want the reason", err)
	}
	if got := name(t, s); got != "" {
		t.Fatalf("%q ran", got)
	}
}

func TestStatusSaysWhyEachBackendIsMissing(t *testing.T) {
	s := open(t, kinds{kvm: unsupported, gvisor: fmt.Errorf("%w: runsc not found", sandbox.ErrUnsupported)}, t.TempDir())
	st := s.Status()
	reasons := map[string]string{}
	for _, b := range st.Backends {
		reasons[b.Name] = b.Reason
		if b.Name == "host" && b.Usable != true {
			t.Errorf("the host backend should be usable here: %+v", b)
		}
	}
	if !strings.Contains(reasons["kvm"], "/dev/kvm") || !strings.Contains(reasons["gvisor"], "runsc") {
		t.Errorf("reasons = %v", reasons)
	}
	if strings.Contains(reasons["kvm"], "unsupported") {
		t.Errorf("the reason repeats the error class: %q", reasons["kvm"])
	}
}

func TestTheChoiceSurvivesARestartAndABrokenFileFallsBackToAuto(t *testing.T) {
	dir := t.TempDir()
	s := open(t, kinds{}, dir)
	if err := s.Set(backends.GVisor); err != nil {
		t.Fatal(err)
	}
	if got := open(t, kinds{}, dir).Status().Setting; got != backends.GVisor {
		t.Fatalf("setting after restart = %q", got)
	}
	info, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("settings file: %v %v", info, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := open(t, kinds{}, dir).Status(); st.Setting != backends.Auto {
		t.Fatalf("a broken file gave %q, want auto", st.Setting)
	}
	// A setting nobody offers is not accepted from the file either, and the
	// host is not selected by a file that says so without a confirmation.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"backend":"host"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := open(t, kinds{}, dir).Status(); st.Setting == backends.Host {
		t.Fatalf("the file selected the host without a confirmation")
	}
	if err := s.Set("anything"); !errors.Is(err, backends.ErrUnknown) {
		t.Fatalf("Set(unknown) = %v", err)
	}
}

func TestSwitchingClosesTheBackendThatWasRunning(t *testing.T) {
	s := open(t, kinds{}, t.TempDir())
	b, err := s.Backend()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(backends.Off); err != nil {
		t.Fatal(err)
	}
	if !b.(*stub).closed.Load() {
		t.Error("the backend was not closed when the choice changed")
	}
}

func TestTheAppArmorReasonNamesTheExactAdminStep(t *testing.T) {
	dir := t.TempDir()
	runsc := filepath.Join(dir, "runsc")
	tool := filepath.Join(dir, "gvisor-profile")
	if err := os.WriteFile(tool, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := fmt.Errorf("%w: runsc cannot make a user namespace (permission denied): an administrator must load the AppArmor profile that gvisor.Profile(%q) returns", sandbox.ErrUnsupported, runsc)
	s := open(t, kinds{kvm: unsupported, gvisor: err}, t.TempDir())
	var got string
	for _, b := range s.Status().Backends {
		if b.Name == "gvisor" {
			got = b.Reason
		}
	}
	for _, want := range []string{tool + " " + runsc, "/etc/apparmor.d/", "apparmor_parser -r", "Ubuntu 24.04"} {
		if !strings.Contains(got, want) {
			t.Errorf("reason lacks %q: %q", want, got)
		}
	}
	if strings.Contains(got, "gvisor.Profile") {
		t.Errorf("reason names a Go function: %q", got)
	}
}

func TestTheAppArmorReasonWithoutTheShippedToolStillNamesTheCommand(t *testing.T) {
	err := fmt.Errorf("%w: runsc cannot make a user namespace (permission denied): an administrator must load the AppArmor profile that gvisor.Profile(%q) returns", sandbox.ErrUnsupported, "/opt/gvisor/runsc")
	s := open(t, kinds{kvm: unsupported, gvisor: err}, t.TempDir())
	var got string
	for _, b := range s.Status().Backends {
		if b.Name == "gvisor" {
			got = b.Reason
		}
	}
	if !strings.Contains(got, "gvisor-profile /opt/gvisor/runsc") || !strings.Contains(got, "apparmor_parser -r") {
		t.Fatalf("reason = %q", got)
	}
}

func TestTheRootRefusalSaysHowToRunUnprivileged(t *testing.T) {
	err := fmt.Errorf("%w: the backend must not run as root on the host: run it as an unprivileged user", sandbox.ErrUnsupported)
	s := open(t, kinds{kvm: err, gvisor: err}, t.TempDir())
	for _, b := range s.Status().Backends {
		if b.Name == "kvm" && (!strings.Contains(b.Reason, "LOCALAI_RUN_AS_ROOT") || !strings.Contains(b.Reason, "unprivileged")) {
			t.Errorf("reason = %q", b.Reason)
		}
	}
}

// A backend sweeps the sandboxes an earlier process left when it starts. It
// starts at the first call, so after a restart nothing was swept until
// someone used the sandbox. Warm starts the chosen backend at once.
func TestWarmStartsTheChosenBackendWithoutACall(t *testing.T) {
	var started atomic.Int32
	mk := func(name string) func() (sandbox.Backend, error) {
		return func() (sandbox.Backend, error) { started.Add(1); return &stub{name: name}, nil }
	}
	s, err := backends.Open(backends.Options{DataDir: t.TempDir(), KVM: mk("kvm"), GVisor: mk("gvisor"), Host: mk("host")})
	if err != nil {
		t.Fatal(err)
	}
	if started.Load() != 0 {
		t.Fatal("Open started a backend")
	}
	s.Warm()
	if started.Load() != 1 {
		t.Fatalf("Warm started %d backends, want the 1 that auto picks", started.Load())
	}
	if got := name(t, s); got != "kvm" || started.Load() != 1 {
		t.Fatalf("after Warm the first call got %q and %d starts: the backend must be reused", got, started.Load())
	}
}

func TestWarmWithNoUsableBackendIsQuiet(t *testing.T) {
	s := open(t, kinds{kvm: unsupported, gvisor: unsupported}, t.TempDir())
	s.Warm() // must not panic or block
	if got := name(t, s); got != "" {
		t.Fatalf("got %q", got)
	}
}
