package model

import "sync"

// ModelStore abstracts model tracking. Single-node mode uses an in-memory map;
// distributed mode backs this with PostgreSQL.
type ModelStore interface {
	Get(id string) (*Model, bool)
	Set(id string, m *Model)
	Delete(id string)
	Range(fn func(id string, m *Model) bool) // return false to stop
}

// InMemoryModelStore is the default ModelStore backed by a plain map.
type InMemoryModelStore struct {
	mu     sync.RWMutex
	models map[string]*Model
}

func NewInMemoryModelStore() *InMemoryModelStore {
	return &InMemoryModelStore{models: make(map[string]*Model)}
}

func (s *InMemoryModelStore) Get(id string) (*Model, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.models[id]
	return m, ok
}

func (s *InMemoryModelStore) Set(id string, m *Model) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models[id] = m
}

func (s *InMemoryModelStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.models, id)
}

func (s *InMemoryModelStore) Range(fn func(string, *Model) bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, m := range s.models {
		if !fn(id, m) {
			return
		}
	}
}
