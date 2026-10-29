// SPDX-License-Identifier: MIT

// Package backends chooses the backend that runs the code of the chats, and
// keeps the administrator's choice. The rules:
//
//   - "auto" picks KVM, then gVisor, then nothing. It never picks the host
//     backend, not even as the last resort.
//   - An explicit choice is never replaced by another when it does not work:
//     the tools then say they cannot run, and the status says why.
//   - The host backend runs code on the host with no isolation. It can be
//     chosen only after its confirmation phrase was typed, and the
//     confirmation is forgotten when another choice is made.
package backends

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/codetools"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// Setting is a choice of backend.
type Setting string

// The choices.
const (
	Auto   Setting = "auto"
	GVisor Setting = "gvisor"
	KVM    Setting = "kvm"
	Off    Setting = "off"
	Host   Setting = "host"
)

// HostPhrase is what an administrator types to accept that commands will run
// on the host with no isolation.
const HostPhrase = "I understand that commands run on this host without isolation"

// Errors.
var (
	ErrUnknown      = errors.New("unknown backend")
	ErrNotConfirmed = errors.New("the host backend needs its confirmation phrase")
)

// probeTTL is how long a failed probe is remembered.
const probeTTL = 30 * time.Second

// Options configures a Selector. Each constructor returns a backend or an
// error that wraps sandbox.ErrUnsupported, with the reason in its text.
type Options struct {
	DataDir string
	KVM     func() (sandbox.Backend, error)
	GVisor  func() (sandbox.Backend, error)
	Host    func() (sandbox.Backend, error)
	Now     func() time.Time
}

// Selector picks the backend for each call.
type Selector struct {
	o        Options
	path     string
	mu       sync.Mutex
	set      settings
	held     map[Setting]sandbox.Backend
	failed   map[Setting]probeFailure
	loadNote string
}

type settings struct {
	Backend       Setting `json:"backend"`
	HostConfirmed bool    `json:"hostConfirmed,omitempty"`
}

type probeFailure struct {
	err error
	at  time.Time
}

// Open reads the saved choice. A missing file means "auto"; a broken file is
// ignored and said in the status.
func Open(o Options) (*Selector, error) {
	if o.DataDir == "" || o.KVM == nil || o.GVisor == nil || o.Host == nil {
		return nil, errors.New("backends: DataDir and the three constructors are required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Selector{o: o, path: filepath.Join(o.DataDir, "settings.json"), held: map[Setting]sandbox.Backend{}, failed: map[Setting]probeFailure{}}
	s.set = settings{Backend: Auto}
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var f settings
		if json.Unmarshal(raw, &f) != nil || !valid(f.Backend) {
			s.loadNote = "The saved choice could not be read, so the backend is chosen automatically."
		} else if f.Backend == Host && !f.HostConfirmed {
			s.loadNote = "The saved choice was the host backend without its confirmation, so the backend is chosen automatically."
		} else {
			s.set = f
		}
	}
	return s, nil
}

func valid(b Setting) bool {
	switch b {
	case Auto, GVisor, KVM, Off, Host:
		return true
	}
	return false
}

func (s *Selector) save() error {
	if err := os.MkdirAll(s.o.DataDir, 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(s.set)
	tmp, err := os.CreateTemp(s.o.DataDir, ".settings-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func (s *Selector) ctor(b Setting) func() (sandbox.Backend, error) {
	switch b {
	case KVM:
		return s.o.KVM
	case GVisor:
		return s.o.GVisor
	case Host:
		return s.o.Host
	}
	return nil
}

// get returns the running backend of a kind, starting it when needed. A kind
// that failed is not tried again until probeTTL has passed. The lock is held.
func (s *Selector) get(b Setting) (sandbox.Backend, error) {
	if h, ok := s.held[b]; ok {
		return h, nil
	}
	if f, ok := s.failed[b]; ok && s.o.Now().Sub(f.at) < probeTTL {
		return nil, f.err
	}
	h, err := s.ctor(b)()
	if err != nil {
		s.failed[b] = probeFailure{err: err, at: s.o.Now()}
		return nil, err
	}
	delete(s.failed, b)
	s.held[b] = h
	return h, nil
}

// closeExcept closes every running backend but keep. The lock is held.
func (s *Selector) closeExcept(keep Setting) {
	for k, h := range s.held {
		if k == keep {
			continue
		}
		if c, ok := h.(interface{ Close() error }); ok {
			_ = c.Close()
		}
		delete(s.held, k)
	}
}

// plan lists the kinds to try for a setting, in order. The lock is held.
func (s *Selector) plan() []Setting {
	switch s.set.Backend {
	case Auto:
		return []Setting{KVM, GVisor}
	case KVM, GVisor:
		return []Setting{s.set.Backend}
	case Host:
		if s.set.HostConfirmed {
			return []Setting{Host}
		}
	}
	return nil
}

// Backend returns the backend to run a call on, or codetools.ErrNoSandbox when
// there is none.
func (s *Selector) Backend() (sandbox.Backend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.plan() {
		b, err := s.get(k)
		if err == nil {
			s.closeExcept(k)
			return b, nil
		}
		if !errors.Is(err, sandbox.ErrUnsupported) {
			return nil, err
		}
	}
	s.closeExcept("")
	return nil, codetools.ErrNoSandbox
}

// Warm starts the backend the next call would use, without a call. A backend
// stops the sandboxes that an earlier process left when it starts, so the
// add-on warms the selector at startup: after a crash or a kill the leftovers
// are removed at once and not at the first tool call, which may never come.
// A backend that cannot start is left to Status and to the next call.
func (s *Selector) Warm() {
	_, _ = s.Backend()
}

// Set saves the choice. The host needs its confirmation first; any other choice
// forgets it.
func (s *Selector) Set(b Setting) error {
	if !valid(b) {
		return fmt.Errorf("%w: %q", ErrUnknown, b)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := settings{Backend: b, HostConfirmed: s.set.HostConfirmed && b == Host}
	if b == Host {
		if !s.set.HostConfirmed {
			return ErrNotConfirmed
		}
		// Say now why it cannot run, rather than at the first call.
		if _, err := s.get(Host); err != nil {
			return errors.New(reason(err))
		}
	}
	prev := s.set
	s.set = next
	if err := s.save(); err != nil {
		s.set = prev
		return err
	}
	s.closeExcept(first(s.plan()))
	return nil
}

func first(l []Setting) Setting {
	if len(l) == 0 {
		return ""
	}
	return l[0]
}

// ConfirmHost records that the phrase was typed. It does not choose the host.
func (s *Selector) ConfirmHost(phrase string) error {
	if phrase != HostPhrase {
		return ErrNotConfirmed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.set
	s.set.HostConfirmed = true
	if err := s.save(); err != nil {
		s.set = prev
		return err
	}
	return nil
}

// reason is the sentence for a failed backend, without the error class.
func reason(err error) string {
	r := strings.TrimPrefix(err.Error(), sandbox.ErrUnsupported.Error()+": ")
	// The backend names its Go function for the profile; an administrator needs
	// the exact commands. The add-on ships the tool next to runsc.
	if i := strings.Index(r, "gvisor.Profile("); i >= 0 {
		if j := strings.Index(r[i:], ")"); j >= 0 {
			path := strings.Trim(r[i+len("gvisor.Profile("):i+j], `"`)
			tool := "gvisor-profile"
			if p := filepath.Join(filepath.Dir(path), "gvisor-profile"); fileExists(p) {
				tool = p
			}
			r = r[:i] + "the profile of " + path + ". Ubuntu 24.04 and later need it because AppArmor blocks user namespaces for unknown programs. " +
				"Run this once as root on the machine that runs the add-on: `" + tool + " " + path +
				" > /etc/apparmor.d/localai-runsc && apparmor_parser -r /etc/apparmor.d/localai-runsc`" + r[i+j+1:]
			r = strings.TrimSuffix(strings.TrimSpace(r), " returns")
		}
	}
	if strings.Contains(r, "must not run as root") {
		r += ". The official LocalAI image starts as an unprivileged user unless LOCALAI_RUN_AS_ROOT=true is set; elsewhere, start LocalAI from an account that is not root"
	}
	return r
}

// BackendState is one backend as the status shows it.
type BackendState struct {
	Name   string `json:"name"`
	Usable bool   `json:"usable"`
	// Reason says what is missing and how to fix it, for a backend that is not
	// usable.
	Reason string `json:"reason,omitempty"`
}

// Status is what the administrator sees.
type Status struct {
	Setting       Setting        `json:"setting"`
	Active        string         `json:"active"`
	Backends      []BackendState `json:"backends"`
	HostConfirmed bool           `json:"hostConfirmed"`
	// HostNotice is true while commands run on the host with no isolation. The
	// page shows it for as long as it is true.
	HostNotice bool   `json:"hostNotice"`
	Note       string `json:"note,omitempty"`
}

// Status probes every backend and says which one runs now.
func (s *Selector) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Setting: s.set.Backend, HostConfirmed: s.set.HostConfirmed, Note: s.loadNote}
	for _, k := range []Setting{KVM, GVisor, Host} {
		_, held := s.held[k]
		b, err := s.get(k)
		if err == nil && !held {
			// A probe of a backend nobody chose must not keep its lock.
			if c, ok := b.(interface{ Close() error }); ok {
				_ = c.Close()
			}
			delete(s.held, k)
		}
		bs := BackendState{Name: string(k), Usable: err == nil}
		if err != nil {
			bs.Reason = reason(err)
		}
		st.Backends = append(st.Backends, bs)
	}
	for _, k := range s.plan() {
		if _, err := s.get(k); err == nil {
			st.Active = string(k)
			break
		}
	}
	st.HostNotice = st.Active == string(Host)
	return st
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
