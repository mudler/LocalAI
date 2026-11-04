// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// maxReceipts bounds the receipts the journal keeps.
const maxReceipts = 512

type journalFile struct {
	Receipts map[string]contract.Receipt `json:"receipts"`
	Order    []string                    `json:"order"`
}

// journal keeps the receipt of each change that was made, in one JSON file of
// the data directory (memory only without one). It holds no key and no text
// of the server.
type journal struct {
	mu   sync.Mutex
	path string
	data journalFile
}

func openJournal(dir string) *journal {
	j := &journal{data: journalFile{Receipts: map[string]contract.Receipt{}}}
	if dir == "" {
		return j
	}
	j.path = filepath.Join(dir, "journal.json")
	if b, err := os.ReadFile(j.path); err == nil {
		var f journalFile
		if json.Unmarshal(b, &f) == nil && f.Receipts != nil {
			j.data = f
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

func (j *journal) commit(r contract.Receipt) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	next := journalFile{Receipts: map[string]contract.Receipt{}, Order: append([]string(nil), j.data.Order...)}
	for k, v := range j.data.Receipts {
		next.Receipts[k] = v
	}
	if _, seen := next.Receipts[r.OperationID]; !seen {
		next.Order = append(next.Order, r.OperationID)
	}
	next.Receipts[r.OperationID] = r
	for len(next.Order) > maxReceipts {
		delete(next.Receipts, next.Order[0])
		next.Order = next.Order[1:]
	}
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
