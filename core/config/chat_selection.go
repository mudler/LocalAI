// SPDX-License-Identifier: MIT
package config

import (
	"maps"
	"sync"
)

// ChatSelection is a detached snapshot. Epoch detects ABA changes within this
// host lifetime; it is not durable authority or an execution permission.
type ChatSelection struct {
	HostExec      string
	ToolProviders map[string]string
	Epoch         uint64
}

type chatSelectionState struct {
	mu    sync.RWMutex
	value ChatSelection
}

// ApplicationConfig is copied by value by embedders. Keep synchronization and
// canonical selection state behind a pointer, never copy a live mutex. The
// initialization lock also supports legacy zero-value configs. Public fields
// remain startup inputs/serialization mirrors, not concurrent mutation APIs.
var chatSelectionInit sync.Mutex

func (o *ApplicationConfig) selectionState() *chatSelectionState {
	chatSelectionInit.Lock()
	defer chatSelectionInit.Unlock()
	if o.chatSelection == nil {
		o.chatSelection = &chatSelectionState{value: ChatSelection{HostExec: o.ChatsHostExec, ToolProviders: maps.Clone(o.ChatsToolProviders)}}
	}
	return o.chatSelection
}

func (o *ApplicationConfig) ChatSelectionSnapshot() ChatSelection {
	s := o.selectionState()
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.value
	v.ToolProviders = maps.Clone(v.ToolProviders)
	return v
}

func (o *ApplicationConfig) SetChatsHostExec(v string) {
	s := o.selectionState()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value.HostExec != v {
		s.value.HostExec = v
		s.value.Epoch++
	}
	o.ChatsHostExec = v
}

func (o *ApplicationConfig) SetChatsToolProviders(v map[string]string) {
	s := o.selectionState()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !maps.Equal(s.value.ToolProviders, v) {
		s.value.ToolProviders = maps.Clone(v)
		s.value.Epoch++
	}
	o.ChatsToolProviders = maps.Clone(v)
}
