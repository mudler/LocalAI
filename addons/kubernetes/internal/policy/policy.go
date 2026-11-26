// SPDX-License-Identifier: MIT

// Package policy keeps the policy of each cluster of the Kubernetes add-on:
// the namespaces the tools may touch, the write tools that work and whether
// Secret values may be read. An administrator sets it on the add-on page. The
// add-on enforces it on every call, whether a person confirmed the call or
// not, so skipping a confirmation never widens what a call can touch.
//
// The file is keyed by the server URL of the cluster, written one way
// (kubeapi.NormalizeServer), so every context and every connection of one
// cluster shares one policy, and renaming a context of the kubeconfig does
// not change it. A cluster with no policy of its own uses the default policy,
// which an administrator can set too. Out of the box that default allows
// reads in every namespace, Secret values included, and no write at all, so
// a new cluster can be read and cannot be changed until an administrator
// decides.
//
// The file lives in the data folder of the add-on, like the settings of the
// other add-ons.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/pkg/addons/sdk/atomicfile"
)

// The write tools a policy can turn on, in the order of the manifest.
var WriteTools = []string{"scale", "rollout-restart", "rollout-undo", "delete-pod", "cordon", "uncordon", "drain", "apply", "patch", "exec"}

// Bounds of the file.
const (
	MaxNamespaces = 50
	MaxClusters   = 100
	// All is the namespace entry that allows every namespace.
	All = "*"
)

// The rules a refusal names.
const (
	RuleNamespaces       = "namespaces"
	RuleWriteTools       = "writeTools"
	RuleClusterScoped    = "clusterScoped"
	RuleReadSecretValues = "readSecretValues"
)

// Refusal is a call the policy refused. The text names the rule, so the model
// can tell the person what to change.
type Refusal struct {
	Rule string
	Text string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("refused by the connection policy (%s): %s", r.Rule, r.Text)
}

// Refuse returns a Refusal.
func Refuse(rule, format string, a ...any) error {
	return &Refusal{Rule: rule, Text: fmt.Sprintf(format, a...)}
}

// Policy is what one cluster allows.
type Policy struct {
	Namespaces       []string `json:"namespaces"`
	WriteTools       []string `json:"writeTools"`
	ReadSecretValues bool     `json:"readSecretValues"`
}

// Defaults is the default policy out of the box: every namespace may be read,
// no write tool works, and Secret values are shown. An administrator can turn
// the values off for a cluster, or in the default policy.
func Defaults() Policy {
	return Policy{Namespaces: []string{All}, WriteTools: []string{}, ReadSecretValues: true}
}

// Damaged is the policy of every cluster while the file cannot be read: the
// defaults with Secret values hidden, since the damaged file may have hidden
// them and a damaged file must never widen access.
func Damaged() Policy {
	p := Defaults()
	p.ReadSecretValues = false
	return p
}

var label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// ValidNamespace reports whether ns is a namespace name.
func ValidNamespace(ns string) bool { return label.MatchString(ns) }

// Normalize checks a policy, drops repeats and sorts the lists.
func (p Policy) Normalize() (Policy, error) {
	if len(p.Namespaces) == 0 {
		return Policy{}, errors.New("name at least one namespace or *")
	}
	if len(p.Namespaces) > MaxNamespaces {
		return Policy{}, fmt.Errorf("at most %d namespaces", MaxNamespaces)
	}
	out := Policy{ReadSecretValues: p.ReadSecretValues, Namespaces: []string{}, WriteTools: []string{}}
	for _, ns := range p.Namespaces {
		if ns != All && !ValidNamespace(ns) {
			return Policy{}, fmt.Errorf("%q is not a namespace name", short(ns))
		}
		if !slices.Contains(out.Namespaces, ns) {
			out.Namespaces = append(out.Namespaces, ns)
		}
	}
	if slices.Contains(out.Namespaces, All) {
		out.Namespaces = []string{All}
	}
	for _, t := range p.WriteTools {
		if !slices.Contains(WriteTools, t) {
			return Policy{}, fmt.Errorf("%q is not a write tool", short(t))
		}
		if !slices.Contains(out.WriteTools, t) {
			out.WriteTools = append(out.WriteTools, t)
		}
	}
	slices.Sort(out.Namespaces)
	slices.Sort(out.WriteTools)
	return out, nil
}

func short(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// AllNamespaces reports whether the policy allows every namespace.
func (p Policy) AllNamespaces() bool { return slices.Contains(p.Namespaces, All) }

// NamespaceAllowed reports whether a namespaced call may touch ns.
func (p Policy) NamespaceAllowed(ns string) bool {
	return p.AllNamespaces() || slices.Contains(p.Namespaces, ns)
}

// ToolEnabled reports whether a write tool works.
func (p Policy) ToolEnabled(tool string) bool { return slices.Contains(p.WriteTools, tool) }

// Hash identifies the policy, for the revision of a write: a write whose
// policy changed after the preview is stale.
func (p Policy) Hash() string {
	n, err := p.Normalize()
	if err != nil {
		n = p
	}
	b, _ := json.Marshal(n)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type file struct {
	// Default applies to a cluster with no policy of its own. Nil is the
	// built-in Defaults.
	Default  *Policy          `json:"default,omitempty"`
	Clusters map[string]Policy `json:"clusters"`
	// Legacy keeps entries of the old per-context key that could not be
	// migrated. They never apply.
	Legacy map[string]Policy `json:"legacy,omitempty"`
}

// Store keeps the policies in policy.json in dir, mode 0600, written
// atomically. It reads the file on each call, so a change applies to the
// next call.
type Store struct {
	mu   sync.Mutex
	path string
	mem  file
	now  func() time.Time
}

// Open returns a store in dir. An empty dir keeps the policies in memory.
func Open(dir string) *Store {
	s := &Store{mem: file{Clusters: map[string]Policy{}}, now: time.Now}
	if dir != "" {
		s.path = filepath.Join(dir, "policy.json")
	}
	return s
}

// ErrUnreadable means the file exists and cannot be read.
var ErrUnreadable = errors.New("the policy file cannot be read")

// ServerKey writes the key of a cluster: its server URL, written one way. It
// also takes a connection key (<server>#context=<name>).
func ServerKey(key string) (string, error) {
	if strings.Contains(key, "#") {
		c, err := kubeapi.ParseKey(key)
		if err != nil {
			return "", err
		}
		return c.Server, nil
	}
	return kubeapi.NormalizeServer(key)
}

// state is the file as read: the policies in force and what could not be
// used.
type state struct {
	f file
	// legacy are entries of the old per-context key that were not
	// migrated: they disagree with another entry of the same server.
	legacy []string
	broken bool
}

func (s *Store) read() state {
	if s.path == "" {
		return migrate(s.mem)
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return state{f: file{Clusters: map[string]Policy{}}}
	}
	if err != nil {
		return state{f: file{Clusters: map[string]Policy{}}, broken: true}
	}
	var f file
	if json.Unmarshal(b, &f) != nil {
		return state{f: file{Clusters: map[string]Policy{}}, broken: true}
	}
	if f.Default != nil {
		n, err := f.Default.Normalize()
		if err != nil {
			return state{f: file{Clusters: map[string]Policy{}}, broken: true}
		}
		f.Default = &n
	}
	for _, m := range []map[string]Policy{f.Clusters, f.Legacy} {
		for k, p := range m {
			n, err := p.Normalize()
			if err != nil {
				return state{f: file{Clusters: map[string]Policy{}}, broken: true}
			}
			m[k] = n
		}
	}
	return migrate(f)
}

// migrate moves entries of the old key (<server>#context=<name>) to the key
// of their server. Entries of one server that disagree are not migrated: they
// stay in the file under legacy, and the server uses the default policy until
// an administrator saves one for it. An old entry of a server that already
// has a policy under the new key is dropped.
func migrate(in file) state {
	out := file{Default: in.Default, Clusters: map[string]Policy{}, Legacy: map[string]Policy{}}
	for k, p := range in.Legacy {
		out.Legacy[k] = p
	}
	old := map[string]map[string]Policy{}
	for k, p := range in.Clusters {
		server, err := ServerKey(k)
		switch {
		case err != nil:
			out.Legacy[k] = p
		case strings.Contains(k, "#"):
			if old[server] == nil {
				old[server] = map[string]Policy{}
			}
			old[server][k] = p
		default:
			out.Clusters[server] = p
		}
	}
	for server, entries := range old {
		if _, own := out.Clusters[server]; own {
			continue
		}
		hashes := map[string]Policy{}
		for _, p := range entries {
			hashes[p.Hash()] = p
		}
		if len(hashes) == 1 {
			for _, p := range hashes {
				out.Clusters[server] = p
			}
			continue
		}
		for k, p := range entries {
			out.Legacy[k] = p
		}
	}
	legacy := make([]string, 0, len(out.Legacy))
	for k := range out.Legacy {
		legacy = append(legacy, k)
	}
	sort.Strings(legacy)
	if len(out.Legacy) == 0 {
		out.Legacy = nil
	}
	return state{f: out, legacy: legacy}
}

// Get returns the policy of one cluster: its own, or the default policy. A
// file that cannot be read gives every cluster the Damaged policy, which
// allows no write and hides Secret values, so a damaged file never widens
// access.
func (s *Store) Get(key string) (Policy, error) {
	server, err := ServerKey(key)
	if err != nil {
		return Policy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.read()
	if st.broken {
		return Damaged(), nil
	}
	if p, ok := st.f.Clusters[server]; ok {
		return p, nil
	}
	if st.f.Default != nil {
		return *st.f.Default, nil
	}
	return Defaults(), nil
}

// View is what the page shows.
type View struct {
	// Clusters holds the clusters that have a policy of their own.
	Clusters map[string]Policy
	// Default applies to every other cluster; DefaultSet says whether an
	// administrator set it.
	Default    Policy
	DefaultSet bool
	// Broken means the file could not be read; every cluster uses the
	// Damaged policy until an administrator saves.
	Broken bool
	// Legacy lists entries of the old per-context key that were not
	// migrated.
	Legacy []string
}

// View returns the policies.
func (s *Store) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.read()
	v := View{Clusters: make(map[string]Policy, len(st.f.Clusters)), Default: Defaults(), Broken: st.broken, Legacy: st.legacy}
	for k, p := range st.f.Clusters {
		v.Clusters[k] = p
	}
	if st.f.Default != nil {
		v.Default, v.DefaultSet = *st.f.Default, true
	}
	if st.broken {
		v.Default = Damaged()
	}
	if v.Legacy == nil {
		v.Legacy = []string{}
	}
	return v
}

// Set stores the policy of one cluster.
func (s *Store) Set(key string, p Policy) (Policy, error) {
	server, err := ServerKey(key)
	if err != nil {
		return Policy{}, err
	}
	return s.save(func(f *file, n Policy) error {
		if _, known := f.Clusters[server]; !known && len(f.Clusters) >= MaxClusters {
			return fmt.Errorf("at most %d clusters have a policy", MaxClusters)
		}
		f.Clusters[server] = n
		for k := range f.Legacy {
			if old, err := ServerKey(k); err == nil && old == server {
				delete(f.Legacy, k)
			}
		}
		return nil
	}, p)
}

// SetDefault stores the default policy.
func (s *Store) SetDefault(p Policy) (Policy, error) {
	return s.save(func(f *file, n Policy) error {
		f.Default = &n
		return nil
	}, p)
}

// save changes the file. A file that cannot be read is kept aside under a
// new name, and the change starts from an empty file: nothing of the damaged
// file is trusted.
func (s *Store) save(change func(*file, Policy) error, p Policy) (Policy, error) {
	n, err := p.Normalize()
	if err != nil {
		return Policy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.read()
	f := st.f
	if err := change(&f, n); err != nil {
		return Policy{}, err
	}
	if len(f.Legacy) == 0 {
		f.Legacy = nil
	}
	if s.path == "" {
		s.mem = f
		return n, nil
	}
	if st.broken {
		aside := fmt.Sprintf("%s.damaged-%d", s.path, s.now().Unix())
		if err := os.Rename(s.path, aside); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Policy{}, errors.New("the damaged policy file could not be kept aside, so nothing was saved")
		}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return Policy{}, err
	}
	if err := atomicfile.Write(s.path, b, 0o600); err != nil {
		return Policy{}, errors.New("the policy could not be saved")
	}
	return n, nil
}
