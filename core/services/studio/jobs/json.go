package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// jsonData is the content of the file.
type jsonData struct {
	Tasks []Task `json:"tasks"`
	Jobs  []Job  `json:"jobs"`
}

// jsonStore keeps tasks and jobs in memory and, with a directory, in the file
// jobs.json (rewritten atomically after every change). One process owns the
// directory: the lock is in-process only, so a cluster uses the GORM store.
type jsonStore struct {
	mu    sync.Mutex
	path  string // empty: memory only
	tasks map[string]Task
	jobs  map[string]Job
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
// With a directory it creates it and loads what is there.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{tasks: map[string]Task{}, jobs: map[string]Job{}}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "jobs.json")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var d jsonData
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("jobs: read %s: %w", s.path, err)
	}
	for _, t := range d.Tasks {
		s.tasks[t.ID] = t
	}
	for _, j := range d.Jobs {
		s.jobs[j.ID] = j
	}
	return s, nil
}

// save writes the file. The caller holds the lock.
func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	d := jsonData{Tasks: make([]Task, 0, len(s.tasks)), Jobs: make([]Job, 0, len(s.jobs))}
	for _, t := range s.tasks {
		d.Tasks = append(d.Tasks, t)
	}
	for _, j := range s.jobs {
		d.Jobs = append(d.Jobs, j)
	}
	sort.Slice(d.Tasks, func(i, k int) bool { return d.Tasks[i].ID < d.Tasks[k].ID })
	sort.Slice(d.Jobs, func(i, k int) bool { return d.Jobs[i].ID < d.Jobs[k].ID })
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}

func (s *jsonStore) PutTask(_ context.Context, t Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.tasks[t.ID]
	if exists && old.Owner != t.Owner {
		return ErrNotFound
	}
	key := NameKey(t.Name)
	for _, o := range s.tasks {
		if o.ID != t.ID && o.Owner == t.Owner && NameKey(o.Name) == key {
			return ErrNameTaken
		}
	}
	if exists {
		t.Created = old.Created
	}
	s.tasks[t.ID] = t.clone()
	if err := s.save(); err != nil {
		if exists {
			s.tasks[t.ID] = old
		} else {
			delete(s.tasks, t.ID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) GetTask(_ context.Context, owner, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.Owner != owner {
		return Task{}, ErrNotFound
	}
	return t.clone(), nil
}

func (s *jsonStore) tasksWhere(keep func(Task) bool) []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Task{}
	for _, t := range s.tasks {
		if keep(t) {
			out = append(out, t.clone())
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].Owner != out[k].Owner {
			return out[i].Owner < out[k].Owner
		}
		if a, b := NameKey(out[i].Name), NameKey(out[k].Name); a != b {
			return a < b
		}
		return out[i].ID < out[k].ID
	})
	return out
}

func (s *jsonStore) ListTasks(_ context.Context, owner string) ([]Task, error) {
	return s.tasksWhere(func(t Task) bool { return t.Owner == owner }), nil
}

func (s *jsonStore) AllTasks(context.Context) ([]Task, error) {
	return s.tasksWhere(func(Task) bool { return true }), nil
}

func (s *jsonStore) DeleteTask(_ context.Context, owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.Owner != owner {
		return ErrNotFound
	}
	delete(s.tasks, id)
	if err := s.save(); err != nil {
		s.tasks[id] = t
		return err
	}
	return nil
}

func (s *jsonStore) PutJob(_ context.Context, j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.jobs[j.ID]
	if exists && old.Owner != j.Owner {
		return ErrNotFound
	}
	s.jobs[j.ID] = j.clone()
	if err := s.save(); err != nil {
		if exists {
			s.jobs[j.ID] = old
		} else {
			delete(s.jobs, j.ID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) GetJob(_ context.Context, owner, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.Owner != owner {
		return Job{}, ErrNotFound
	}
	return j.clone(), nil
}

func (s *jsonStore) jobsWhere(keep func(Job) bool, limit int) []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Job{}
	for _, j := range s.jobs {
		if keep(j) {
			out = append(out, j.clone())
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if !out[i].Created.Equal(out[k].Created) {
			return out[i].Created.After(out[k].Created)
		}
		return out[i].ID > out[k].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *jsonStore) ListJobs(_ context.Context, owner string, f Filter) ([]Job, error) {
	return s.jobsWhere(func(j Job) bool { return j.Owner == owner && (f.TaskID == "" || j.TaskID == f.TaskID) }, f.Limit), nil
}

func (s *jsonStore) AllJobs(context.Context) ([]Job, error) {
	return s.jobsWhere(func(Job) bool { return true }, 0), nil
}

func (s *jsonStore) DeleteJob(_ context.Context, owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.Owner != owner {
		return ErrNotFound
	}
	delete(s.jobs, id)
	if err := s.save(); err != nil {
		s.jobs[id] = j
		return err
	}
	return nil
}
