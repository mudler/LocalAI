// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// maxDatasetBytes bounds the inline content of one dataset. A tool payload is
// capped at 1 MiB; the larger datasets go through the UI upload.
const maxDatasetBytes = 900 << 10

// maxIssuedRefs bounds the references the host remembers. A restart forgets
// them, and the dataset must then be sent again.
const maxIssuedRefs = 256

// DatasetStore keeps a dataset in the fine-tuning dataset service. Put returns
// the reference the add-on gets: the base name of the stored file, never a
// path. Resolve turns a reference into the service's local path.
type DatasetStore interface {
	Put(name string, data []byte) (ref string, err error)
	Resolve(ref string) (path string, ok bool)
}

// datasetIndex records the user each reference was issued to. The store cannot
// tell users apart, so the host checks the owner before it resolves.
type datasetIndex struct {
	store DatasetStore
	mu    sync.Mutex
	owner map[string]string
	order []string
}

func (x *datasetIndex) issue(user, ref string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, ok := x.owner[ref]; !ok {
		x.order = append(x.order, ref)
	}
	x.owner[ref] = user
	for len(x.order) > maxIssuedRefs {
		delete(x.owner, x.order[0])
		x.order = x.order[1:]
	}
}

// resolve answers the stored path of a reference only for the user it was
// issued to. Another user, or an unknown reference, gets no path.
func (x *datasetIndex) resolve(user, ref string) (string, bool) {
	x.mu.Lock()
	o, ok := x.owner[ref]
	x.mu.Unlock()
	if !ok || o != user {
		return "", false
	}
	return x.store.Resolve(ref)
}

// datasetRefShape matches a reference the host issues: the eight hex digits of
// the store's file prefix, a dash and a name. A datasetSource of this shape is
// a reference, never a Hugging Face id.
var datasetRefShape = regexp.MustCompile(`^[0-9a-f]{8}-[^/\\]+$`)

// RegisterEngineDatasets registers finetune.dataset, which stores a small
// inline text dataset and returns a reference to it. A nil store registers
// nothing.
func RegisterEngineDatasets(b *Broker, ds DatasetStore) error {
	if ds == nil {
		return nil
	}
	idx := &datasetIndex{store: ds, owner: map[string]string{}}
	b.datasets = idx
	return b.Register(Spec{Op: "finetune.dataset", Scope: "apply", Feature: featureFineTuning, Idempotent: true, Handler: func(_ context.Context, c Call) (json.RawMessage, error) {
		// Checked on the raw bytes: the JSON decoder replaces an invalid byte
		// with U+FFFD, so a check on the decoded string would never refuse one.
		if !utf8.Valid(c.Arguments) {
			return nil, fmt.Errorf("%w: content must be UTF-8 text", ErrBadRequest)
		}
		var a struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := decodeArgs(c.Arguments, &a); err != nil {
			return nil, err
		}
		if !datasetName(a.Name) {
			return nil, fmt.Errorf("%w: name must be a file name ending in .jsonl, .json, .csv or .txt", ErrBadRequest)
		}
		if a.Content == "" || len(a.Content) > maxDatasetBytes {
			return nil, fmt.Errorf("%w: content must be between 1 byte and 900 KiB", ErrBadRequest)
		}
		ref, err := idx.store.Put(a.Name, []byte(a.Content))
		if err != nil {
			return nil, err
		}
		idx.issue(userKey(c), ref)
		return marshal(map[string]string{"datasetRef": ref})
	}})
}

// datasetName accepts a plain file name with a text dataset extension. A
// directory part, a parent segment or any other extension does not match.
func datasetName(s string) bool {
	if s == "" || len(s) > maxRefBytes || strings.ContainsAny(s, `/\`) || s == "." || s == ".." {
		return false
	}
	for _, ext := range []string{".jsonl", ".json", ".csv", ".txt"} {
		if strings.HasSuffix(s, ext) && len(s) > len(ext) {
			return true
		}
	}
	return false
}

// resolveDataset answers the path of a dataset reference the user was issued.
// It is nil-safe: without a dataset store no reference resolves.
func (b *Broker) resolveDataset(user, ref string) (string, bool) {
	if b.datasets == nil {
		return "", false
	}
	return b.datasets.resolve(user, ref)
}
