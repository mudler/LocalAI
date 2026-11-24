package p2prelay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// stateFile is the name of the persisted state in Config.DataDir.
const stateFile = "allowlist.json"

const stateVersion = 1

// member is one entry of an admin's set.
type member struct {
	Label string    `json:"label,omitempty"`
	Added time.Time `json:"added"`
	// PendingUntil is set on a member admitted by a relay pass and not yet
	// confirmed by its admin. The member is dropped at that time.
	PendingUntil *time.Time `json:"pending_until,omitempty"`
}

func (m member) pending() bool { return m.PendingUntil != nil }

// live reports whether m admits its peer at now.
func (m member) live(now time.Time) bool { return m.PendingUntil == nil || now.Before(*m.PendingUntil) }

// pass is a relay pass. Only the SHA-256 of the code is kept, so the file
// does not hold usable codes.
type pass struct {
	Label   string    `json:"label,omitempty"`
	Expires time.Time `json:"expires"`
	// UsedBy is the peer that used the pass; a used pass is never accepted
	// again and is kept so that pass-cancel can find that peer.
	UsedBy string `json:"used_by,omitempty"`
	// Keep is when the entry may be deleted.
	Keep time.Time `json:"keep"`
}

// adminSet is the namespace of one admin.
type adminSet struct {
	Seq     int64             `json:"seq"`
	Updated time.Time         `json:"updated"`
	Peers   map[string]member `json:"peers"`
	// Passes is keyed by the hex SHA-256 of the code.
	Passes map[string]pass `json:"passes,omitempty"`
}

func newAdminSet() *adminSet {
	return &adminSet{Peers: map[string]member{}, Passes: map[string]pass{}}
}

// clone returns a copy of s, or an empty set for nil, that shares nothing
// with s.
func (s *adminSet) clone() *adminSet {
	if s == nil {
		return newAdminSet()
	}
	c := *s
	c.Peers = maps.Clone(s.Peers)
	c.Passes = maps.Clone(s.Passes)
	if c.Peers == nil {
		c.Peers = map[string]member{}
	}
	if c.Passes == nil {
		c.Passes = map[string]pass{}
	}
	return &c
}

// state is the content of allowlist.json.
type state struct {
	Version       int                  `json:"version"`
	ClaimedAdmins []string             `json:"claimed_admins,omitempty"`
	Admins        map[string]*adminSet `json:"admins"`
}

func loadState(path string) (state, error) {
	st := state{Version: stateVersion, Admins: map[string]*adminSet{}}
	b, err := os.ReadFile(path) // #nosec G304 -- path is the relay's own state file
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("p2prelay: read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("p2prelay: parse %s: %w", path, err)
	}
	if st.Version != stateVersion {
		return st, fmt.Errorf("p2prelay: %s has version %d, want %d", path, st.Version, stateVersion)
	}
	if st.Admins == nil {
		st.Admins = map[string]*adminSet{}
	}
	for _, s := range st.Admins {
		if s.Peers == nil {
			s.Peers = map[string]member{}
		}
		if s.Passes == nil {
			s.Passes = map[string]pass{}
		}
	}
	return st, nil
}

// saveState writes the state atomically with mode 0600.
func saveState(path string, st *state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("p2prelay: encode state: %w", err)
	}
	return writeFileAtomic(path, b)
}

// writeFileAtomic replaces path with b, with mode 0600, so a crash leaves
// the old file or the new one.
func writeFileAtomic(path string, b []byte) error {
	if err := atomicfile.Write(path, b, 0o600); err != nil {
		return fmt.Errorf("p2prelay: save %s: %w", filepath.Base(path), err)
	}
	return nil
}
