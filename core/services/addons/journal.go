// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type journalOperation struct {
	Command LifecycleCommand
	Actor   contract.Actor
	Status  OperationStatus
	Next    *Status
}
type journal struct {
	Version    int
	Addons     map[string]Status
	Operations map[string]journalOperation
}

// Each transition replaces one complete snapshot. File and directory fsyncs
// make the journal's state and operation receipt a single durability boundary.
func durableWrite(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".journal-")
	if e != nil {
		return e
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
func (m *Manager) persist(phase string) error {
	b, e := json.Marshal(m.journal)
	if e == nil {
		e = durableWrite(filepath.Join(m.config.Root, "journal.json"), b)
	}
	if e != nil {
		m.uncertain = true
		return e
	}
	if m.boundary != nil {
		if e = m.boundary(phase); e != nil {
			m.uncertain = true
			return e
		}
	}
	return nil
}
func (m *Manager) load() error {
	b, e := os.ReadFile(filepath.Join(m.config.Root, "journal.json"))
	if os.IsNotExist(e) {
		m.journal = journal{Version: 1, Addons: map[string]Status{}, Operations: map[string]journalOperation{}}
		return nil
	}
	if e != nil {
		return e
	}
	var j journal
	if e = json.Unmarshal(b, &j); e != nil {
		return e
	}
	if j.Version != 1 || j.Addons == nil || j.Operations == nil {
		return ErrConfiguration
	}
	m.journal = j
	return nil
}
func copyStatus(s Status) Status {
	b, _ := json.Marshal(s)
	var out Status
	_ = json.Unmarshal(b, &out)
	return out
}
