package loops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// jsonStore keeps everything in memory and, with a directory, in the file
// loops.json, rewritten atomically after every change. The lock is in-process
// only: one process owns the directory, so a cluster uses the GORM store.
type jsonStore struct {
	mu   sync.Mutex
	path string
	d    jsonData
}

type jsonData struct {
	Loops    map[string]Loop    `json:"loops"`
	Days     map[string]jsonDay `json:"days"`     // by owner
	Consents map[string]bool    `json:"consents"` // by owner + "\x00" + chat
}

type jsonDay struct {
	Day  string `json:"day"`
	Runs int    `json:"runs"`
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{}
	s.d.init()
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "loops.json")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.d); err != nil {
		return nil, fmt.Errorf("loops: read %s: %w", s.path, err)
	}
	s.d.init()
	return s, nil
}

func (d *jsonData) init() {
	if d.Loops == nil {
		d.Loops = map[string]Loop{}
	}
	if d.Days == nil {
		d.Days = map[string]jsonDay{}
	}
	if d.Consents == nil {
		d.Consents = map[string]bool{}
	}
}

func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.d)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}

func consentKey(owner, chatID string) string { return owner + "\x00" + chatID }

// norm keeps times in UTC, the way the GORM store reads them back.
func norm(l Loop) Loop {
	for _, t := range []*time.Time{&l.Next, &l.Created, &l.Updated, &l.LastRun} {
		if !t.IsZero() {
			*t = t.UTC()
		}
	}
	return l
}

func (s *jsonStore) Add(_ context.Context, l Loop, lim Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Loops[l.ID]; ok {
		return fmt.Errorf("%w: loop %s exists", ErrInvalid, l.ID)
	}
	inChat, ofOwner := 0, 0
	for _, x := range s.d.Loops {
		if x.Owner != l.Owner || !x.State.Live() {
			continue
		}
		ofOwner++
		if x.ChatID == l.ChatID {
			inChat++
		}
	}
	if err := checkLimits(inChat, ofOwner, lim); err != nil {
		return err
	}
	s.d.Loops[l.ID] = norm(l)
	return s.save()
}

// checkLimits says whether one more loop fits, given the live loops the chat
// and the owner have.
func checkLimits(inChat, ofOwner int, lim Limits) error {
	if lim.PerChat > 0 && inChat >= lim.PerChat {
		return ErrTooManyInChat
	}
	if lim.PerUser > 0 && ofOwner >= lim.PerUser {
		return ErrTooManyForOwner
	}
	return nil
}

func (s *jsonStore) Get(_ context.Context, id string) (Loop, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.d.Loops[id]
	if !ok {
		return Loop{}, ErrNotFound
	}
	return l, nil
}

func (s *jsonStore) Update(_ context.Context, id string, change func(*Loop) error) (Loop, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.d.Loops[id]
	if !ok {
		return Loop{}, ErrNotFound
	}
	next := cur
	if err := change(&next); err != nil {
		return Loop{}, err
	}
	next.ID, next.Owner, next.ChatID = cur.ID, cur.Owner, cur.ChatID
	if err := next.Validate(); err != nil {
		return Loop{}, err
	}
	next = norm(next)
	s.d.Loops[id] = next
	if err := s.save(); err != nil {
		s.d.Loops[id] = cur
		return Loop{}, err
	}
	return next, nil
}

func (s *jsonStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Loops[id]; !ok {
		return nil
	}
	delete(s.d.Loops, id)
	return s.save()
}

func (s *jsonStore) list(keep func(Loop) bool) []Loop {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Loop{}
	for _, l := range s.d.Loops {
		if keep(l) {
			out = append(out, l)
		}
	}
	sortLoops(out)
	return out
}

func sortLoops(out []Loop) {
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
}

func (s *jsonStore) ListChat(_ context.Context, owner, chatID string) ([]Loop, error) {
	return s.list(func(l Loop) bool { return l.Owner == owner && l.ChatID == chatID }), nil
}

func (s *jsonStore) ListOwner(_ context.Context, owner string) ([]Loop, error) {
	return s.list(func(l Loop) bool { return l.Owner == owner }), nil
}

func (s *jsonStore) Working(context.Context) ([]Loop, error) {
	return s.list(func(l Loop) bool { return l.State == StateActive || l.Running }), nil
}

func (s *jsonStore) DeleteChat(_ context.Context, owner, chatID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, l := range s.d.Loops {
		if l.Owner == owner && l.ChatID == chatID {
			delete(s.d.Loops, id)
			n++
		}
	}
	_, consented := s.d.Consents[consentKey(owner, chatID)]
	delete(s.d.Consents, consentKey(owner, chatID))
	if n == 0 && !consented {
		return 0, nil
	}
	return n, s.save()
}

func (s *jsonStore) TakeRun(_ context.Context, owner, day string, budget int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.d.Days[owner]
	if cur.Day != day {
		cur = jsonDay{Day: day}
	}
	if budget > 0 && cur.Runs >= budget {
		return false, nil
	}
	cur.Runs++
	s.d.Days[owner] = cur
	return true, s.save()
}

func (s *jsonStore) Consented(_ context.Context, owner, chatID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Consents[consentKey(owner, chatID)], nil
}

func (s *jsonStore) SetConsented(_ context.Context, owner, chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Consents[consentKey(owner, chatID)] {
		return nil
	}
	s.d.Consents[consentKey(owner, chatID)] = true
	return s.save()
}
