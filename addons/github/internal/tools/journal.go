// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// maxReceipts bounds the receipts the journal keeps. The host asks for a
// receipt only for a recent, unsettled operation.
const maxReceipts = 512

// journalFile holds, per operation, the receipt of a finished change and the
// time an attempt was started. It never holds a token or any text sent to
// GitHub.
type journalFile struct {
	Receipts map[string]contract.Receipt `json:"receipts"`
	Started  map[string]time.Time        `json:"started"`
	Order    []string                    `json:"order"`
}

// journal is one JSON file in the data directory, written through a temporary
// file and renamed. Without a directory it lives in memory only.
type journal struct {
	mu   sync.Mutex
	path string
	data journalFile
}

func openJournal(dir string) *journal {
	j := &journal{data: journalFile{Receipts: map[string]contract.Receipt{}, Started: map[string]time.Time{}}}
	if dir == "" {
		return j
	}
	j.path = filepath.Join(dir, "journal.json")
	if b, err := os.ReadFile(j.path); err == nil {
		var f journalFile
		if json.Unmarshal(b, &f) == nil {
			if f.Receipts != nil {
				j.data.Receipts = f.Receipts
			}
			if f.Started != nil {
				j.data.Started = f.Started
			}
			j.data.Order = f.Order
		}
	}
	return j
}

func (j *journal) receipt(op string) (contract.Receipt, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, ok := j.data.Receipts[op]
	return r, ok
}

func (j *journal) started(op string) (time.Time, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	t, ok := j.data.Started[op]
	return t, ok
}

// save writes next and, when the write worked, makes it the state. A failed
// write leaves the memory as it was.
func (j *journal) save(next journalFile) error {
	if j.path != "" {
		b, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if err := atomicfile.Write(j.path, b, 0o600); err != nil {
			return err
		}
	}
	j.data = next
	return nil
}

func (j *journal) copy() journalFile {
	n := journalFile{Receipts: map[string]contract.Receipt{}, Started: map[string]time.Time{}, Order: append([]string(nil), j.data.Order...)}
	for k, v := range j.data.Receipts {
		n.Receipts[k] = v
	}
	for k, v := range j.data.Started {
		n.Started[k] = v
	}
	return n
}

// begin notes, before the request leaves, that an attempt started. If the
// note cannot be stored the request is not sent.
func (j *journal) begin(op string, at time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := j.copy()
	n.Started[op] = at
	return j.save(n)
}

func (j *journal) forget(op string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := j.copy()
	delete(n.Started, op)
	_ = j.save(n)
}

func (j *journal) commit(r contract.Receipt) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := j.copy()
	delete(n.Started, r.OperationID)
	if _, seen := n.Receipts[r.OperationID]; !seen {
		n.Order = append(n.Order, r.OperationID)
	}
	n.Receipts[r.OperationID] = r
	for len(n.Order) > maxReceipts {
		delete(n.Receipts, n.Order[0])
		n.Order = n.Order[1:]
	}
	return j.save(n)
}
