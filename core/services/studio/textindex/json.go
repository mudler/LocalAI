package textindex

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// jsonStore keeps the entries in a folder: one small file per entry for
// what it says about the node (meta/<id>.json), one file per text
// (texts/<id>.txt) and cursor.json. The metadata is held in memory, the texts
// are not. Search reads the texts under the prefix one by one and matches
// them in memory: a scan, not an index, which is fine for the one process
// that owns the folder. A cluster uses the GORM store. The whole texts of
// long documents are files too (documents/<hash>-<extractor>.txt).
type jsonStore struct {
	mu     sync.Mutex
	dir    string // empty: memory only
	meta   map[int64]Entry
	texts  map[int64]string  // memory only
	docs   map[string]string // memory only, by documentKey
	cursor int64
}

func documentKey(hash string, extractor int) string {
	return hash + "-" + strconv.Itoa(extractor)
}

type jsonCursor struct {
	Rev int64 `json:"rev"`
}

// NewJSON returns a Store in dir. With an empty dir it keeps everything in
// memory.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{dir: dir, meta: map[int64]Entry{}, texts: map[int64]string{}, docs: map[string]string{}}
	if dir == "" {
		return s, nil
	}
	for _, d := range []string{"meta", "texts", "documents"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			return nil, err
		}
	}
	files, err := os.ReadDir(filepath.Join(dir, "meta"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "meta", f.Name())) // #nosec G304 -- dir is the index directory the host configured; the name comes from listing it
		if err != nil {
			return nil, err
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("textindex: read %s: %w", f.Name(), err)
		}
		s.meta[e.NodeID] = e
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cursor.json")) // #nosec G304 -- a fixed name in the index directory the host configured
	switch {
	case err == nil:
		var c jsonCursor
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("textindex: read the cursor: %w", err)
		}
		s.cursor = c.Rev
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	return s, nil
}

func (s *jsonStore) metaPath(id int64) string {
	return filepath.Join(s.dir, "meta", strconv.FormatInt(id, 10)+".json")
}

func (s *jsonStore) textPath(id int64) string {
	return filepath.Join(s.dir, "texts", strconv.FormatInt(id, 10)+".txt")
}

func (s *jsonStore) saveMeta(e Entry) error {
	if s.dir == "" {
		return nil
	}
	e.Text = ""
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.metaPath(e.NodeID), raw, 0o600)
}

func (s *jsonStore) readText(id int64) (string, error) {
	if s.dir == "" {
		return s.texts[id], nil
	}
	raw, err := os.ReadFile(s.textPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(raw), err
}

func (s *jsonStore) Put(_ context.Context, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		s.texts[e.NodeID] = e.Text
	} else {
		// The text first: a crash between the two leaves an old entry that
		// points at a newer text of the same node, which the hash check of a
		// search turns away.
		if e.Text == "" {
			if err := os.Remove(s.textPath(e.NodeID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		} else if err := atomicfile.Write(s.textPath(e.NodeID), []byte(e.Text), 0o600); err != nil {
			return err
		}
		if err := s.saveMeta(e); err != nil {
			return err
		}
	}
	e.Text = ""
	e.Sections = slices.Clone(e.Sections)
	s.meta[e.NodeID] = e
	return nil
}

func (s *jsonStore) Get(_ context.Context, id int64) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.meta[id]
	if !ok {
		return Entry{}, ErrNotFound
	}
	text, err := s.readText(id)
	if err != nil {
		return Entry{}, err
	}
	e.Text = text
	e.Sections = slices.Clone(e.Sections)
	return e, nil
}

func (s *jsonStore) ReadyByHash(_ context.Context, hash string, extractor int) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.sortedIDs() {
		e := s.meta[id]
		if e.Hash == hash && e.Extractor == extractor && e.Status == StatusReady {
			text, err := s.readText(id)
			if err != nil {
				return Entry{}, false, err
			}
			e.Text = text
			e.Sections = slices.Clone(e.Sections)
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

func (s *jsonStore) Delete(_ context.Context, ids ...int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if _, ok := s.meta[id]; !ok {
			continue
		}
		if s.dir != "" {
			for _, p := range []string{s.metaPath(id), s.textPath(id)} {
				if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
		}
		delete(s.meta, id)
		delete(s.texts, id)
	}
	return nil
}

func (s *jsonStore) Move(_ context.Context, from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.meta {
		p, ok := moved(e.Path, from, to)
		if !ok {
			continue
		}
		e.Path = p
		if err := s.saveMeta(e); err != nil {
			return err
		}
		s.meta[id] = e
	}
	return nil
}

func (s *jsonStore) sortedIDs() []int64 {
	ids := slices.Collect(maps.Keys(s.meta))
	slices.Sort(ids)
	return ids
}

func (s *jsonStore) Pending(_ context.Context, prefix string, limit int) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range s.meta {
		if e.Status == StatusPending && Under(e.Path, prefix) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if c := a.Updated.Compare(b.Updated); c != 0 {
			return c
		}
		return cmp.Compare(a.NodeID, b.NodeID)
	})
	if limit = limitOr(limit, defaultLimit); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *jsonStore) List(_ context.Context, prefix string, afterID int64, limit int) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit = limitOr(limit, defaultListLimit)
	var out []Entry
	for _, id := range s.sortedIDs() {
		if e := s.meta[id]; id > afterID && Under(e.Path, prefix) {
			e.Sections = slices.Clone(e.Sections)
			if out = append(out, e); len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (s *jsonStore) Search(ctx context.Context, q Query) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := limitOr(q.Limit, defaultLimit)
	var out []Entry
	for _, id := range s.sortedIDs() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e := s.meta[id]
		if id <= q.AfterID || e.Status != StatusReady || !Under(e.Path, q.Prefix) {
			continue
		}
		text, err := s.readText(id)
		if err != nil {
			return nil, err
		}
		if q.Contains != "" && !strings.Contains(strings.ToLower(text), q.Contains) {
			continue
		}
		e.Text = text
		e.Sections = slices.Clone(e.Sections)
		if out = append(out, e); len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *jsonStore) CountPending(_ context.Context, prefix string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.meta {
		if e.Status == StatusPending && Under(e.Path, prefix) {
			n++
		}
	}
	return n, nil
}

// documentPath is the file of a whole text. A hash is a blob hash (hex), and
// one that is not is refused, so a key never leaves the folder.
func (s *jsonStore) documentPath(hash string, extractor int) (string, error) {
	if hash == "" || strings.ContainsAny(hash, `/\.`) {
		return "", fmt.Errorf("textindex: invalid hash %q", hash)
	}
	return filepath.Join(s.dir, "documents", documentKey(hash, extractor)+".txt"), nil
}

func (s *jsonStore) PutDocument(_ context.Context, hash string, extractor int, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		s.docs[documentKey(hash, extractor)] = text
		return nil
	}
	p, err := s.documentPath(hash, extractor)
	if err != nil {
		return err
	}
	return atomicfile.Write(p, []byte(text), 0o600)
}

func (s *jsonStore) Document(_ context.Context, hash string, extractor int) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		text, ok := s.docs[documentKey(hash, extractor)]
		return text, ok, nil
	}
	p, err := s.documentPath(hash, extractor)
	if err != nil {
		return "", false, err
	}
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

func (s *jsonStore) PruneDocuments(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := map[string]bool{}
	for _, e := range s.meta {
		live[e.Hash] = true
	}
	// A key is <hash>-<extractor>; the hash has no dash.
	hashOf := func(key string) string {
		h, _, _ := strings.Cut(key, "-")
		return h
	}
	if s.dir == "" {
		for k := range s.docs {
			if !live[hashOf(k)] {
				delete(s.docs, k)
			}
		}
		return nil
	}
	files, err := os.ReadDir(filepath.Join(s.dir, "documents"))
	if err != nil {
		return err
	}
	for _, f := range files {
		key, ok := strings.CutSuffix(f.Name(), ".txt")
		if !ok || live[hashOf(key)] {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, "documents", f.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *jsonStore) Cursor(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor, nil
}

func (s *jsonStore) SetCursor(_ context.Context, rev int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		raw, _ := json.Marshal(jsonCursor{Rev: rev})
		if err := atomicfile.Write(filepath.Join(s.dir, "cursor.json"), raw, 0o600); err != nil {
			return err
		}
	}
	s.cursor = rev
	return nil
}
