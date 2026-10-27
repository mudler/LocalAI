package backend

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Route every operation through the backend so model checks, reset and reranking
// are shared with REST callers, including adapters created before a reset.
type backendRAGAdapter struct {
	backend    *backendInProcess
	collection string
	// Agent config keys retain their case even though collection paths do not.
	settingsName string
}

var _ RAGDB = (*backendRAGAdapter)(nil)

func (a *backendRAGAdapter) Store(content string) error {
	settings, err := a.backend.settings(a.settingsName)
	if err != nil {
		return err
	}
	if err := a.backend.createCollection(a.collection, settings); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%x.txt", time.Now().Format("2006-01-02-15-04-05"), sha256.Sum256([]byte(content)))
	_, err = a.backend.upload(a.collection, name, strings.NewReader(content), settings)
	return err
}
func (a *backendRAGAdapter) Reset() error { return a.backend.Reset(a.collection) }
func (a *backendRAGAdapter) Search(query string, n int) ([]string, error) {
	settings, err := a.backend.settings(a.settingsName)
	if err != nil {
		return nil, err
	}
	if err := a.backend.createCollection(a.collection, settings); err != nil {
		return nil, err
	}
	results, err := a.backend.search(a.collection, query, n, settings)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, fmt.Sprintf("%s (%+v)", r.Content, r.Metadata))
	}
	return out, nil
}
func (a *backendRAGAdapter) Count() int {
	kb, ok := a.backend.lookup(a.collection)
	if !ok {
		return 0
	}
	return kb.Count()
}

type backendCompactionAdapter struct{ *backendRAGAdapter }

var _ KBCompactionClient = (*backendCompactionAdapter)(nil)

func (a *backendCompactionAdapter) Collection() string { return a.collection }
func (a *backendCompactionAdapter) ListEntries() ([]string, error) {
	entries, err := a.backend.ListEntries(a.collection)
	if err != nil {
		return nil, err
	}
	for i, entry := range entries {
		entries[i] = filepath.Base(entry)
	}
	return entries, nil
}
func (a *backendCompactionAdapter) GetEntryContent(entry string) (string, int, error) {
	return a.backend.GetEntryContent(a.collection, entry)
}
func (a *backendCompactionAdapter) Store(path string) error {
	settings, err := a.backend.settings(a.settingsName)
	if err != nil {
		return err
	}
	if err := a.backend.createCollection(a.collection, settings); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = a.backend.upload(a.collection, filepath.Base(path), file, settings)
	return err
}
func (a *backendCompactionAdapter) DeleteEntry(entry string) error {
	_, err := a.backend.DeleteEntry(a.collection, entry)
	return err
}
