// SPDX-License-Identifier: MIT

// Package journal keeps what an add-on must remember across a restart: the
// receipt of each change it made and the time an attempt was started. It is
// one JSON file in the data directory, written through a temporary file and
// renamed. Without a directory it lives in memory only. It never holds a
// token or any text of the service the add-on talks to.
package journal

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// DefaultMaxReceipts bounds the receipts the journal keeps. The host asks for
// a receipt only for a recent, unsettled operation.
const DefaultMaxReceipts = 512

// NoJobs is the job type of an add-on that keeps no per-resource state.
type NoJobs struct{}

type file[J any] struct {
	Jobs     map[string]J                `json:"jobs,omitempty"`
	Receipts map[string]contract.Receipt `json:"receipts"`
	Started  map[string]time.Time        `json:"started"`
	Order    []string                    `json:"order"`
}

// Store is the journal of one add-on. J is what the add-on keeps for each
// resource it created, such as a job it started.
type Store[J any] struct {
	mu          sync.Mutex
	path        string
	maxReceipts int
	data        file[J]
}

// Journal is the journal of an add-on that keeps receipts only.
type Journal = Store[NoJobs]

// Option changes a journal at Open.
type Option func(*options)

type options struct{ maxReceipts int }

// WithMaxReceipts changes how many receipts the journal keeps. A repeat of an
// older operation is then not answered from memory any more.
func WithMaxReceipts(n int) Option {
	return func(o *options) { o.maxReceipts = n }
}

// Open returns the journal in dir, or a memory-only one when dir is empty.
func Open(dir string, opts ...Option) *Journal { return OpenJobs[NoJobs](dir, opts...) }

// OpenJobs is Open for an add-on that also keeps one J per resource.
func OpenJobs[J any](dir string, opts ...Option) *Store[J] {
	o := options{maxReceipts: DefaultMaxReceipts}
	for _, opt := range opts {
		opt(&o)
	}
	s := &Store[J]{maxReceipts: o.maxReceipts, data: empty[J]()}
	if dir == "" {
		return s
	}
	s.path = filepath.Join(dir, "journal.json")
	b, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var f file[J]
	if json.Unmarshal(b, &f) != nil {
		return s
	}
	if f.Jobs != nil {
		s.data.Jobs = f.Jobs
	}
	if f.Receipts != nil {
		s.data.Receipts = f.Receipts
	}
	if f.Started != nil {
		s.data.Started = f.Started
	}
	s.data.Order = f.Order
	return s
}

func empty[J any]() file[J] {
	return file[J]{
		Jobs:     map[string]J{},
		Receipts: map[string]contract.Receipt{},
		Started:  map[string]time.Time{},
	}
}

// Receipt returns the receipt of a finished operation.
func (s *Store[J]) Receipt(op string) (contract.Receipt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data.Receipts[op]
	return r, ok
}

// Started returns when an attempt of op was noted and not settled.
func (s *Store[J]) Started(op string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.data.Started[op]
	return t, ok
}

// Job returns what the add-on keeps for a resource.
func (s *Store[J]) Job(resource string) (J, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.data.Jobs[resource]
	return j, ok
}

// save writes next and, when the write worked, makes it the state. A failed
// write leaves the memory as it was.
func (s *Store[J]) save(next file[J]) error {
	if s.path != "" {
		b, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if err := atomicfile.Write(s.path, b, 0o600); err != nil {
			return err
		}
	}
	s.data = next
	return nil
}

func (s *Store[J]) clone() file[J] {
	return file[J]{
		Jobs:     maps.Clone(s.data.Jobs),
		Receipts: maps.Clone(s.data.Receipts),
		Started:  maps.Clone(s.data.Started),
		Order:    slices.Clone(s.data.Order),
	}
}

// Begin notes, before the request leaves, that an attempt started. If the
// note cannot be stored the request is not sent.
func (s *Store[J]) Begin(op string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.clone()
	next.Started[op] = at
	return s.save(next)
}

// Forget drops the note of an attempt that is known not to have left.
func (s *Store[J]) Forget(op string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.clone()
	delete(next.Started, op)
	_ = s.save(next)
}

// Commit stores the receipt of an operation and clears its attempt note.
func (s *Store[J]) Commit(r contract.Receipt) error {
	return s.CommitJob(r, "", nil)
}

// CommitJob stores the receipt and, when job is not nil, the new state of a
// resource in one write. A failed write leaves the memory as it was, so the
// caller reports the failure and nothing claims a state that is not on disk.
func (s *Store[J]) CommitJob(r contract.Receipt, resource string, job *J) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.clone()
	if job != nil {
		next.Jobs[resource] = *job
	}
	delete(next.Started, r.OperationID)
	if _, seen := next.Receipts[r.OperationID]; !seen {
		next.Order = append(next.Order, r.OperationID)
	}
	next.Receipts[r.OperationID] = r
	for len(next.Order) > s.maxReceipts {
		delete(next.Receipts, next.Order[0])
		next.Order = next.Order[1:]
	}
	return s.save(next)
}
