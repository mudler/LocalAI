package backend

import (
	"crypto/md5" // #nosec G501 -- the hash only names a temporary file and protects nothing
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/localrecall/rag"
	"github.com/mudler/xlog"
)

// internalRAGAdapter implements RAGDB by calling the in-process *rag.PersistentKB directly.
type internalRAGAdapter struct {
	mu         sync.RWMutex
	collection string
	kb         *rag.PersistentKB
}

var _ RAGDB = (*internalRAGAdapter)(nil)

func (a *internalRAGAdapter) Store(s string) error {
	a.mu.RLock()
	kb := a.kb
	a.mu.RUnlock()
	if kb == nil {
		return fmt.Errorf("collection not available")
	}
	t := time.Now()
	dateTime := t.Format("2006-01-02-15-04-05")
	hash := md5.Sum([]byte(s)) // #nosec G401 -- the hash only names a temporary file and protects nothing
	fileName := fmt.Sprintf("%s-%s.txt", dateTime, hex.EncodeToString(hash[:]))
	tempdir, err := os.MkdirTemp("", "localrag")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempdir)
	f := filepath.Join(tempdir, fileName)
	if err := os.WriteFile(f, []byte(s), 0644); err != nil { // #nosec G306 -- the file is a temporary copy that kb.Store reads and removes
		return err
	}
	meta := map[string]string{"created_at": t.Format(time.RFC3339)}
	_, err = kb.Store(f, meta)
	return err
}

func (a *internalRAGAdapter) Reset() error {
	a.mu.RLock()
	kb := a.kb
	a.mu.RUnlock()
	if kb == nil {
		return fmt.Errorf("collection not available")
	}
	return kb.Reset()
}

func (a *internalRAGAdapter) Search(s string, similarEntries int) ([]string, error) {
	a.mu.RLock()
	kb := a.kb
	a.mu.RUnlock()
	if kb == nil {
		return nil, fmt.Errorf("collection not available")
	}
	results, err := kb.Search(s, similarEntries)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, fmt.Sprintf("%s (%+v)", r.Content, r.Metadata))
	}
	return out, nil
}

func (a *internalRAGAdapter) Count() int {
	a.mu.RLock()
	kb := a.kb
	a.mu.RUnlock()
	if kb == nil {
		return 0
	}
	return kb.Count()
}

// internalCompactionAdapter implements KBCompactionClient for the same in-process collection.
type internalCompactionAdapter struct {
	mu         sync.RWMutex
	collection string
	kb         *rag.PersistentKB
}

var _ KBCompactionClient = (*internalCompactionAdapter)(nil)

func (a *internalCompactionAdapter) Collection() string {
	return a.collection
}

func (a *internalCompactionAdapter) ListEntries() ([]string, error) {
	a.mu.RLock()
	kb := a.kb
	a.mu.RUnlock()
	if kb == nil {
		return nil, fmt.Errorf("collection not available")
	}
	keys := kb.ListDocuments()
	entries := make([]string, len(keys))
	for i, k := range keys {
		entries[i] = filepath.Base(k)
	}
	return entries, nil
}

func (a *internalCompactionAdapter) GetEntryContent(entry string) (content string, chunkCount int, err error) {
	a.mu.RLock()
	kb := a.kb
	a.mu.RUnlock()
	if kb == nil {
		return "", 0, fmt.Errorf("collection not available")
	}
	return kb.GetEntryFileContent(entry)
}

func (a *internalCompactionAdapter) Store(filePath string) error {
	a.mu.RLock()
	kb := a.kb
	a.mu.RUnlock()
	if kb == nil {
		return fmt.Errorf("collection not available")
	}
	meta := map[string]string{"created_at": time.Now().Format(time.RFC3339)}
	_, err := kb.Store(filePath, meta)
	return err
}

func (a *internalCompactionAdapter) DeleteEntry(entry string) error {
	a.mu.RLock()
	kb := a.kb
	a.mu.RUnlock()
	if kb == nil {
		return fmt.Errorf("collection not available")
	}
	return kb.RemoveEntry(entry)
}

// RAGProviderFromState returns a RAG provider function from a State.
// External consumers (e.g. LocalAI) can call NewInProcessBackend to get the state,
// then pass it here to create a RAG provider for the agent pool.
func RAGProviderFromState(cs *State) func(collectionName string) (RAGDB, KBCompactionClient, bool) {
	return func(collectionName string) (RAGDB, KBCompactionClient, bool) {
		if cs == nil {
			return nil, nil, false
		}
		name := strings.TrimSpace(strings.ToLower(collectionName))
		if name == "" {
			return nil, nil, false
		}
		// Pool construction holds its config mutex. Defer collection creation and
		// model resolution until an operation, when that mutex is no longer held.
		if cs.backend != nil {
			adapter := &backendRAGAdapter{backend: cs.backend, collection: name, settingsName: collectionName}
			return adapter, &backendCompactionAdapter{adapter}, true
		}
		var kb *rag.PersistentKB
		cs.Mu.RLock()
		kb, ok := cs.Collections[name]
		ensure := cs.EnsureCollection
		cs.Mu.RUnlock()
		if !ok || kb == nil {
			if ensure == nil {
				xlog.Debug("internal RAG: no ensureCollection", "collection", name)
				return nil, nil, false
			}
			var created bool
			kb, created = ensure(name)
			if !created || kb == nil {
				xlog.Debug("internal RAG: ensure collection failed", "collection", name)
				return nil, nil, false
			}
		}
		ragAdapter := &internalRAGAdapter{collection: name, kb: kb}
		compAdapter := &internalCompactionAdapter{collection: name, kb: kb}
		return ragAdapter, compAdapter, true
	}
}
