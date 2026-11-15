package remotes

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

// ownedEntry is one model config that a remote owns.
type ownedEntry struct {
	// Name is the model config name.
	Name string `json:"name"`
	// UpstreamID is the model id at the remote.
	UpstreamID string `json:"upstream_id"`
	// Hash is the sha256 of the entry file as the service last wrote it. A
	// file with another hash was edited by a person.
	Hash string `json:"hash,omitempty"`
}

// record is what a store keeps for one remote. It is never sent over
// HTTP: Sealed is the key, encrypted.
type record struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Kind        Kind            `json:"kind"`
	URL         string          `json:"url"`
	Status      Status          `json:"status"`
	LastError   string          `json:"last_error,omitempty"`
	LastRefresh *time.Time      `json:"last_refresh,omitempty"`
	Owned       []ownedEntry    `json:"owned,omitempty"`
	Unavailable []string        `json:"unavailable,omitempty"`
	New         []UpstreamModel `json:"new,omitempty"`
	Created     time.Time       `json:"created"`
	// Sealed is the encrypted key; nil means no key.
	Sealed []byte `json:"sealed,omitempty"`
}

func (r record) clone() record {
	r.Owned = slices.Clone(r.Owned)
	r.Unavailable = slices.Clone(r.Unavailable)
	r.New = slices.Clone(r.New)
	r.Sealed = slices.Clone(r.Sealed)
	if r.LastRefresh != nil {
		t := *r.LastRefresh
		r.LastRefresh = &t
	}
	return r
}

// view returns the public form of the record.
func (r record) view() Remote {
	c := Remote{
		ID: r.ID, Name: r.Name, Kind: r.Kind, URL: r.URL,
		KeySet: len(r.Sealed) > 0, Status: r.Status, LastError: r.LastError,
		Entries:     make([]string, 0, len(r.Owned)),
		Unavailable: slices.Clone(r.Unavailable),
		New:         slices.Clone(r.New),
	}
	if c.Status == "" {
		c.Status = StatusNever
	}
	if c.Unavailable == nil {
		c.Unavailable = []string{}
	}
	if c.New == nil {
		c.New = []UpstreamModel{}
	}
	if r.LastRefresh != nil {
		t := *r.LastRefresh
		c.LastRefresh = &t
	}
	for _, o := range r.Owned {
		c.Entries = append(c.Entries, o.Name)
	}
	return c
}

func nameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Store keeps the remote records. A store holds no cache: every call
// reads the shared state, so any frontend sees what another wrote.
type Store interface {
	// Create adds a record. It fails with ErrConflict when the id or the
	// name (compared without case) is taken.
	Create(ctx context.Context, r record) error
	Get(ctx context.Context, id string) (record, error)
	List(ctx context.Context) ([]record, error)
	// Update reads the record, lets fn change it and writes it back as one
	// step, so two writers do not lose each other's change. If fn fails,
	// nothing is written. A new name that is taken fails with ErrConflict.
	Update(ctx context.Context, id string, fn func(*record) error) (record, error)
	// Delete removes a record. When check is not nil it runs on the
	// current record in the same step, and an error from it keeps the record
	// and is returned, so a condition cannot change between the check and
	// the delete.
	Delete(ctx context.Context, id string, check func(record) error) error
	// EnsureKey returns the sealing key. The first call stores what generate
	// returns; every later call, from any frontend, returns that key. It
	// refuses to make a new key while sealed records exist: those were sealed
	// with a key that is now missing and would never open again.
	EnsureKey(ctx context.Context, generate func() ([]byte, error)) ([]byte, error)
}

// errKeyMissing is returned by EnsureKey when sealed records exist but the
// key that sealed them is gone.
var errKeyMissing = errors.New("remotes: the sealing key is missing but stored keys were sealed with it; restore the key (remotes.key, or the seal row of the database) instead of letting a new one be made")

// anySealed says whether a record holds a sealed key.
func anySealed(rs map[string]record) bool {
	for _, r := range rs {
		if len(r.Sealed) > 0 {
			return true
		}
	}
	return false
}
