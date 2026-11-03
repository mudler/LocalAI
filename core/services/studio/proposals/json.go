package proposals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// maxFileEvents bounds the audit that the file store keeps.
const maxFileEvents = 5000

type fileData struct {
	Proposals []contract.ChangeProposal `json:"proposals"`
	Events    []Event                   `json:"events"`
}

type jsonStore struct {
	mu   sync.Mutex
	path string // empty: memory only
	d    fileData
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory;
// otherwise it loads and rewrites the file proposals.json in dir. One process
// owns the directory; a cluster uses GORM.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("proposals: folder: %w", err)
	}
	s.path = filepath.Join(dir, "proposals.json")
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("proposals: read %s: %w", s.path, err)
	default:
		if err := json.Unmarshal(raw, &s.d); err != nil {
			return nil, fmt.Errorf("proposals: read %s: %w", s.path, err)
		}
	}
	return s, nil
}

// save writes the file; the caller holds the lock.
func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	return atomicfile.WriteJSON(s.path, s.d, 0o600)
}

func (s *jsonStore) find(owner, id string) int {
	return slices.IndexFunc(s.d.Proposals, func(p contract.ChangeProposal) bool { return p.ID == id && p.Owner == owner })
}

func (s *jsonStore) Create(_ context.Context, p contract.ChangeProposal, lim Limits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, recent := 0, 0
	for _, o := range s.d.Proposals {
		if o.ID == p.ID {
			return ErrExists
		}
		if o.Owner != p.Owner {
			continue
		}
		if o.Status == contract.ProposalPending {
			pending++
		}
		if o.Agent == p.Agent && lim.Window > 0 && o.Created.After(p.Created.Add(-lim.Window)) {
			recent++
		}
	}
	if lim.MaxPending > 0 && pending >= lim.MaxPending {
		return ErrTooManyPending
	}
	if lim.PerWindow > 0 && recent >= lim.PerWindow {
		return ErrRateLimited
	}
	s.d.Proposals = append(s.d.Proposals, p)
	if err := s.save(); err != nil {
		s.d.Proposals = s.d.Proposals[:len(s.d.Proposals)-1]
		return err
	}
	return nil
}

func (s *jsonStore) Get(_ context.Context, owner, id string) (contract.ChangeProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(owner, id)
	if i < 0 {
		return contract.ChangeProposal{}, ErrNotFound
	}
	return s.d.Proposals[i], nil
}

func (s *jsonStore) List(_ context.Context, owner string, statuses ...contract.ProposalStatus) ([]contract.ChangeProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contract.ChangeProposal
	for _, p := range s.d.Proposals {
		if p.Owner == owner && (len(statuses) == 0 || slices.Contains(statuses, p.Status)) {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b contract.ChangeProposal) int { return b.Created.Compare(a.Created) })
	return out, nil
}

func (s *jsonStore) Settle(_ context.Context, owner, id string, from, to contract.ProposalStatus, at time.Time, rev int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(owner, id)
	if i < 0 {
		return false, ErrNotFound
	}
	old := s.d.Proposals[i]
	if old.Status != from {
		return false, nil
	}
	p := old
	p.Status, p.Answered = to, at
	if to == contract.ProposalReverted {
		p.RevertRev = rev
	}
	s.d.Proposals[i] = p
	if err := s.save(); err != nil {
		s.d.Proposals[i] = old
		return false, err
	}
	return true, nil
}

func (s *jsonStore) SetRev(_ context.Context, owner, id string, rev int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(owner, id)
	if i < 0 {
		return ErrNotFound
	}
	old := s.d.Proposals[i]
	s.d.Proposals[i].Rev = rev
	if err := s.save(); err != nil {
		s.d.Proposals[i] = old
		return err
	}
	return nil
}

func (s *jsonStore) Expire(_ context.Context, now time.Time) ([]contract.ChangeProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := slices.Clone(s.d.Proposals)
	var out []contract.ChangeProposal
	for i, p := range s.d.Proposals {
		if p.Status == contract.ProposalPending && !p.Expires.After(now) {
			s.d.Proposals[i].Status, s.d.Proposals[i].Answered = contract.ProposalExpired, now
			out = append(out, s.d.Proposals[i])
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	if err := s.save(); err != nil {
		s.d.Proposals = before
		return nil, err
	}
	return out, nil
}

func (s *jsonStore) AddEvent(_ context.Context, ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Events = append(s.d.Events, ev)
	if n := len(s.d.Events) - maxFileEvents; n > 0 {
		s.d.Events = slices.Clone(s.d.Events[n:])
	}
	return s.save()
}

func (s *jsonStore) Events(_ context.Context, owner string, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Event{}
	for i := len(s.d.Events) - 1; i >= 0 && len(out) < limit; i-- {
		if s.d.Events[i].Owner == owner {
			out = append(out, s.d.Events[i])
		}
	}
	return out, nil
}
