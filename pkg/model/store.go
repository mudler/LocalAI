package model

import (
	"io"
	"sync"
)

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

// Delete removes the model and closes the connection of its client. The client
// keeps one connection for all calls, and nothing else closes it when the model
// unloads.
func (s *InMemoryModelStore) Delete(id string) {
	s.mu.Lock()
	m := s.models[id]
	delete(s.models, id)
	s.mu.Unlock()
	if m != nil {
		m.closeClient()
	}
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

// closeClient closes the connection of the client of m, if it has one. A call
// that is still running keeps it open until it ends.
func (m *Model) closeClient() {
	m.Lock()
	c := m.client
	m.Unlock()
	if closer, ok := c.(io.Closer); ok {
		_ = closer.Close() // the model is gone; a close error changes nothing
	}
}
