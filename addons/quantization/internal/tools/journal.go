// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// maxReceipts bounds the receipts the journal keeps. A repeat of an older
// operation is not answered from memory any more; the host asks for a receipt
// only for a recent, unsettled operation.
const maxReceipts = 1024

// job is what the add-on knows of one resource a tool created: a job it
// started. The engine job ID never leaves the add-on: a model names the job by
// its resource.
type job struct {
	JobID string `json:"jobId,omitempty"`
	Name  string `json:"name"`
	// Revision counts the changes a tool made to the job. It is the data
	// revision of the resource, so a stale approval is noticed.
	Revision uint64 `json:"revision"`
}

type journalFile struct {
	Jobs     map[string]job              `json:"jobs"`
	Receipts map[string]contract.Receipt `json:"receipts"`
	Order    []string                    `json:"order"`
}

// journal maps resource IDs to engine job IDs and keeps the receipts of the
// operations that changed something. It is one JSON file in the data
// directory, written through a temporary file and renamed over the old one.
type journal struct {
	mu   sync.Mutex
	path string
	data journalFile
}

func openJournal(dir string) *journal {
	j := &journal{path: filepath.Join(dir, "journal.json")}
	j.data = journalFile{Jobs: map[string]job{}, Receipts: map[string]contract.Receipt{}}
	if b, err := os.ReadFile(j.path); err == nil {
		var f journalFile
		if json.Unmarshal(b, &f) == nil {
			if f.Jobs != nil {
				j.data.Jobs = f.Jobs
			}
			if f.Receipts != nil {
				j.data.Receipts = f.Receipts
			}
			j.data.Order = f.Order
		}
	}
	return j
}

func (j *journal) job(resource string) (job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	v, ok := j.data.Jobs[resource]
	return v, ok
}

func (j *journal) receipt(op string) (contract.Receipt, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, ok := j.data.Receipts[op]
	return r, ok
}

// commit stores the receipt of an operation and, when upd is not nil, the new
// state of a job in one write. A failed write leaves the memory as it was, so
// the caller reports the failure and nothing claims a state that is not on disk.
func (j *journal) commit(resource string, upd *job, r contract.Receipt) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	next := journalFile{Jobs: map[string]job{}, Receipts: map[string]contract.Receipt{}, Order: append([]string(nil), j.data.Order...)}
	for k, v := range j.data.Jobs {
		next.Jobs[k] = v
	}
	for k, v := range j.data.Receipts {
		next.Receipts[k] = v
	}
	if upd != nil {
		next.Jobs[resource] = *upd
	}
	if _, seen := next.Receipts[r.OperationID]; !seen {
		next.Order = append(next.Order, r.OperationID)
	}
	next.Receipts[r.OperationID] = r
	for len(next.Order) > maxReceipts {
		delete(next.Receipts, next.Order[0])
		next.Order = next.Order[1:]
	}
	if err := writeAtomic(j.path, next); err != nil {
		return err
	}
	j.data = next
	return nil
}

func writeAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "journal-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(e error) error { _ = tmp.Close(); _ = os.Remove(name); return e }
	if _, err := tmp.Write(b); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return errors.Join(err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
