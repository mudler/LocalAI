// SPDX-License-Identifier: MIT

// Package policy keeps the policy of each cluster of the Kubernetes add-on:
// the namespaces the tools may touch, the write tools that work and whether
// Secret values may be read. An administrator sets it on the add-on page. The
// add-on enforces it on every call, whether a person confirmed the call or
// not, so skipping a confirmation never widens what a call can touch.
//
// The file is keyed by the cluster key (<server>#context=<name>), the
// resource the connection lists, so two connections of one cluster share one
// policy. It lives in the data folder of the add-on, like the settings of the
// other add-ons: each node that runs the add-on reads its own copy.
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
	"sync"

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

// Defaults allow every namespace and every write tool except exec, and hide
// Secret values.
func Defaults() Policy {
	return Policy{Namespaces: []string{All}, WriteTools: slices.DeleteFunc(slices.Clone(WriteTools), func(s string) bool { return s == "exec" })}
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
	Clusters map[string]Policy `json:"clusters"`
}

// Store keeps the policies in policy.json in dir, mode 0600, written
// atomically. It reads the file on each call, so a change applies to the
// next call.
type Store struct {
	mu   sync.Mutex
	path string
	mem  file
}

// Open returns a store in dir. An empty dir keeps the policies in memory.
func Open(dir string) *Store {
	s := &Store{mem: file{Clusters: map[string]Policy{}}}
	if dir != "" {
		s.path = filepath.Join(dir, "policy.json")
	}
	return s
}

// ErrUnreadable means the file exists and cannot be read. A caller refuses
// every tool then: a broken policy must never widen access.
var ErrUnreadable = errors.New("the policy file cannot be read")

func (s *Store) read() (file, error) {
	if s.path == "" {
		return s.mem, nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return file{Clusters: map[string]Policy{}}, nil
	}
	if err != nil {
		return file{}, ErrUnreadable
	}
	var f file
	if json.Unmarshal(b, &f) != nil {
		return file{}, ErrUnreadable
	}
	if f.Clusters == nil {
		f.Clusters = map[string]Policy{}
	}
	for k, p := range f.Clusters {
		n, err := p.Normalize()
		if err != nil {
			return file{}, ErrUnreadable
		}
		f.Clusters[k] = n
	}
	return f, nil
}

// Get returns the policy of one cluster; the defaults for a cluster with none.
func (s *Store) Get(key string) (Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return Policy{}, err
	}
	if p, ok := f.Clusters[key]; ok {
		return p, nil
	}
	return Defaults(), nil
}

// All returns the policy of every cluster that has one.
func (s *Store) All() (map[string]Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Policy, len(f.Clusters))
	for k, p := range f.Clusters {
		out[k] = p
	}
	return out, nil
}

// Set stores the policy of one cluster. A damaged file is replaced.
func (s *Store) Set(key string, p Policy) (Policy, error) {
	n, err := p.Normalize()
	if err != nil {
		return Policy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		f = file{Clusters: map[string]Policy{}}
	}
	if _, known := f.Clusters[key]; !known && len(f.Clusters) >= MaxClusters {
		return Policy{}, fmt.Errorf("at most %d clusters have a policy", MaxClusters)
	}
	f.Clusters[key] = n
	if s.path == "" {
		s.mem = file{Clusters: f.Clusters}
		return n, nil
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
