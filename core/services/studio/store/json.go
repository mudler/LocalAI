package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/atomicfile"
	"github.com/mudler/xlog"
)

// jsonStore keeps chats in memory and, when it has a directory, mirrors them to
// files: one small record file per chat (rewritten atomically), one
// append-only transcript file per chat and one harness state file per chat
// (written only when the state changes). One process owns the directory: the
// locks below are in-process only, so two LocalAI processes must not share it.
// A cluster uses the GORM store.
//
// mu guards the map only. Each chat has its own lock, held for the whole
// read-modify-write of that chat including its file I/O, so the operations on
// one chat serialize and the operations on different chats do not wait for
// each other. A goroutine that holds a chat lock may take mu (DeleteChat);
// one that holds mu never takes a chat lock.
type jsonStore struct {
	mu    sync.Mutex
	dir   string // empty: memory only
	chats map[string]*chatSlot
}

// chatSlot is one chat with its lock. gone is set, under the lock, when the
// chat is deleted, so a caller that found the slot before the delete and
// waited for the lock sees that it is gone.
type chatSlot struct {
	mu   sync.Mutex
	gone bool
	chatState
}

type chatState struct {
	// Rec.HarnessState is not in the record file: it is in the state file of
	// generation StateGen. A record without StateGen keeps it inline, as the
	// records written before the state file existed did.
	Rec         session.ChatRecord `json:"rec"`
	StateGen    int64              `json:"state_gen,omitempty"`
	Pending     []Pending          `json:"pending"`
	NextPending int64              `json:"next_pending"`
	Run         string             `json:"run,omitempty"`
	RunSince    time.Time          `json:"run_since,omitempty"`
	RunBeat     time.Time          `json:"run_beat,omitempty"`
	Last        TurnStatus         `json:"last,omitempty"`

	entries []session.Entry
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory and
// loses it on exit. With a directory it creates it, loads what is there, and
// writes every change.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{dir: dir, chats: map[string]*chatSlot{}}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(s.chatDir(), 0o700); err != nil {
		return nil, err
	}
	names, err := filepath.Glob(filepath.Join(s.chatDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	for _, p := range names {
		// One damaged chat must not disable the feature for everyone: skip it,
		// keep its files for inspection, and load the rest.
		raw, err := os.ReadFile(p) // #nosec G304 -- the path is a file of the store directory
		if err != nil {
			xlog.Warn("studio store: skipping unreadable chat record", "file", filepath.Base(p), "error", err)
			continue
		}
		var c chatState
		if err := json.Unmarshal(raw, &c); err != nil || c.Rec.ID == "" {
			xlog.Warn("studio store: skipping damaged chat record", "file", filepath.Base(p), "error", err)
			continue
		}
		if c.Run != "" {
			// The process that held the claim is gone; the turn did not finish.
			c.Run, c.Last = "", TurnInterrupted
		}
		// What a request took and nobody confirmed was lost with the process,
		// the same reset ReapTurns applies.
		for i := range c.Pending {
			if c.Pending[i].Unconfirmed {
				c.Pending[i].Delivered, c.Pending[i].Unconfirmed = false, false
			}
		}
		if err := s.loadEntries(&c); err != nil {
			xlog.Warn("studio store: skipping chat with unreadable transcript", "chat", c.Rec.ID, "error", err)
			continue
		}
		if err := s.loadState(&c); err != nil {
			xlog.Warn("studio store: skipping chat with unreadable harness state", "chat", c.Rec.ID, "error", err)
			continue
		}
		s.chats[c.Rec.ID] = &chatSlot{chatState: c}
	}
	s.removeStaleStates()
	return s, nil
}

// stateFile is the harness state file of generation gen of chat id.
func (s *jsonStore) stateFile(id string, gen int64) string {
	return s.file(id, fmt.Sprintf(".%d.state", gen))
}

// loadState reads the harness state of c from its state file. A record written
// before the state file existed holds the state inline; it is moved to a state
// file now, so the record file stays small from here on.
func (s *jsonStore) loadState(c *chatState) error {
	if c.StateGen == 0 {
		if len(c.Rec.HarnessState) == 0 {
			return nil
		}
		next := *c
		if err := s.writeState(&next, c.Rec.HarnessState); err != nil {
			return fmt.Errorf("move the harness state to its own file: %w", err)
		}
		if err := s.save(&next); err != nil {
			_ = os.Remove(s.stateFile(c.Rec.ID, next.StateGen))
			return fmt.Errorf("move the harness state to its own file: %w", err)
		}
		*c = next
		return nil
	}
	raw, err := os.ReadFile(s.stateFile(c.Rec.ID, c.StateGen)) // #nosec G304 -- the path is a file of the store directory
	if err != nil {
		return err
	}
	c.Rec.HarnessState = raw
	return nil
}

// writeState writes state as the next generation of the state file of c and
// moves c to it. The record file still names the old generation until the
// caller saves c, so a crash in between leaves the old state in force and a
// stray file that the next start removes.
func (s *jsonStore) writeState(c *chatState, state []byte) error {
	c.Rec.HarnessState = append([]byte(nil), state...)
	if s.dir == "" {
		return nil
	}
	gen := c.StateGen + 1
	if err := atomicfile.Write(s.stateFile(c.Rec.ID, gen), state, 0o600); err != nil {
		return err
	}
	c.StateGen = gen
	return nil
}

// removeStaleStates deletes the state files that no loaded record names: the
// old generations that a crash left behind, and the files of chats that did
// not load are kept for inspection.
func (s *jsonStore) removeStaleStates() {
	names, err := filepath.Glob(filepath.Join(s.chatDir(), "*.state"))
	if err != nil {
		return
	}
	keep := map[string]bool{}
	skipped := map[string]bool{}
	for _, c := range s.chats {
		if c.StateGen > 0 {
			keep[s.stateFile(c.Rec.ID, c.StateGen)] = true
		}
	}
	for _, p := range names {
		base := filepath.Base(p)
		i := strings.IndexByte(base, '.')
		if i < 0 {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(base[:i])
		if err != nil {
			continue
		}
		if _, ok := s.chats[string(raw)]; !ok {
			skipped[p] = true
		}
		if keep[p] || skipped[p] {
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			xlog.Warn("studio store: could not remove an old harness state file", "file", base, "error", err)
		}
	}
}

func (s *jsonStore) chatDir() string { return filepath.Join(s.dir, "chats") }

func (s *jsonStore) file(id, ext string) string {
	return filepath.Join(s.chatDir(), base64.RawURLEncoding.EncodeToString([]byte(id))+ext)
}

// loadEntries reads the transcript of c. A crash or a full disk can leave a
// half-written last line, and the next append would then follow it on the same
// line and damage a good entry. So a trailing line that is torn or does not
// parse is dropped and the file is cut back to the last good newline.
func (s *jsonStore) loadEntries(c *chatState) error {
	path := s.file(c.Rec.ID, ".entries")
	raw, err := os.ReadFile(path) // #nosec G304 -- the path is a file of the store directory
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	good := 0 // length of the prefix that holds only complete, valid lines
	warned := false
	for off := 0; off < len(raw); {
		end := bytes.IndexByte(raw[off:], '\n')
		terminated := end >= 0
		line := raw[off:]
		next := len(raw)
		if terminated {
			line = raw[off : off+end]
			next = off + end + 1
		}
		var e session.Entry
		if err := json.Unmarshal(line, &e); err != nil {
			if len(bytes.TrimSpace(raw[next:])) == 0 {
				// The last line: a torn write.
				if !warned {
					xlog.Warn("studio store: dropping torn last line of transcript", "chat", c.Rec.ID)
				}
				break
			}
			// A damaged line in the middle: keep the entries around it.
			xlog.Warn("studio store: skipping damaged transcript line", "chat", c.Rec.ID, "offset", off)
			warned = true
			good = next
			off = next
			continue
		}
		c.entries = append(c.entries, e)
		good = next
		off = next
		if !terminated {
			// A complete entry that lost only its newline: restore it so the
			// next append starts on its own line.
			raw = append(raw, '\n')
			good = len(raw)
			break
		}
	}
	if good != len(raw) || (len(raw) > 0 && raw[len(raw)-1] != '\n') {
		return repairTranscript(path, raw[:good])
	}
	return nil
}

// repairTranscript rewrites the transcript with only its good prefix.
func repairTranscript(path string, good []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- the path is a file of the store directory
	if err != nil {
		return err
	}
	_, werr := f.Write(good)
	if serr := f.Sync(); werr == nil {
		werr = serr
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// repairTranscriptCreate is repairTranscript for a file that may not exist yet.
func repairTranscriptCreate(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o600) // #nosec G304 -- the path is a file of the store directory
	if err != nil {
		return err
	}
	_, werr := f.Write(body)
	if serr := f.Sync(); werr == nil {
		werr = serr
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// save rewrites the record file of c atomically. The harness state is not in
// it once the chat has a state file (see writeState).
func (s *jsonStore) save(c *chatState) error {
	if s.dir == "" {
		return nil
	}
	rec := *c
	if rec.StateGen > 0 {
		rec.Rec.HarnessState = nil
	}
	raw, err := json.Marshal(&rec)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.file(c.Rec.ID, ".json"), raw, 0o600)
}

// appendFile is the part of an open file that appendTo needs.
type appendFile interface {
	io.Writer
	Truncate(size int64) error
}

// appendLines appends data, whole lines, to the file at p.
func appendLines(p string, data []byte) error {
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	var size int64
	if fi, err := f.Stat(); err == nil {
		size = fi.Size()
	} else {
		_ = f.Close()
		return err
	}
	err = appendTo(f, size, data)
	return errors.Join(err, f.Close())
}

// appendTo writes data after size bytes. When it fails it cuts the
// file back to size: a short write left in place would put the next entry on
// the same line, and the loader drops a damaged line, so both would be lost.
func appendTo(f appendFile, size int64, data []byte) error {
	_, err := f.Write(data)
	if err != nil {
		if terr := f.Truncate(size); terr != nil {
			err = errors.Join(err, terr)
		}
	}
	return err
}

// lock returns chat id with its lock held. The caller calls unlock. A chat
// that does not exist, or was deleted while the caller waited, is
// session.ErrNotFound.
func (s *jsonStore) lock(id string) (c *chatSlot, unlock func(), err error) {
	s.mu.Lock()
	c, ok := s.chats[id]
	s.mu.Unlock()
	if !ok {
		return nil, nil, session.ErrNotFound
	}
	c.mu.Lock()
	if c.gone {
		c.mu.Unlock()
		return nil, nil, session.ErrNotFound
	}
	return c, c.mu.Unlock, nil
}

// get is lock for a chat that principal owns.
func (s *jsonStore) get(principal, id string) (c *chatSlot, unlock func(), err error) {
	c, unlock, err = s.lock(id)
	if err != nil {
		return nil, nil, err
	}
	if c.Rec.Principal != principal {
		unlock()
		return nil, nil, session.ErrNotFound
	}
	return c, unlock, nil
}

// slots returns the chats that exist now. The caller locks each one it reads
// and skips the ones that are gone.
func (s *jsonStore) slots() []*chatSlot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*chatSlot, 0, len(s.chats))
	for _, c := range s.chats {
		out = append(out, c)
	}
	return out
}

// records returns a copy of the record of every chat that keep accepts.
func (s *jsonStore) records(keep func(session.ChatRecord) bool) []session.ChatRecord {
	var out []session.ChatRecord
	for _, c := range s.slots() {
		c.mu.Lock()
		if !c.gone && keep(c.Rec) {
			out = append(out, cloneRec(c.Rec))
		}
		c.mu.Unlock()
	}
	return out
}

// CreateChat holds the map lock while it writes the new chat's files: the ID
// must stay taken until the chat exists or the write failed. Creating a chat
// is rare next to the other operations.
func (s *jsonStore) CreateChat(_ context.Context, rec session.ChatRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.chats[rec.ID]; ok {
		return session.ErrExists
	}
	c := chatState{Rec: rec}
	c.Rec.HarnessState = nil
	if len(rec.HarnessState) > 0 {
		if err := s.writeState(&c, rec.HarnessState); err != nil {
			return err
		}
	}
	if err := s.save(&c); err != nil {
		if c.StateGen > 0 {
			_ = os.Remove(s.stateFile(rec.ID, c.StateGen))
		}
		return err
	}
	s.chats[rec.ID] = &chatSlot{chatState: c}
	return nil
}

func (s *jsonStore) GetChat(_ context.Context, principal, id string) (session.ChatRecord, error) {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return session.ChatRecord{}, err
	}
	defer unlock()
	return cloneRec(c.Rec), nil
}

func cloneRec(r session.ChatRecord) session.ChatRecord {
	r.HarnessState = append([]byte(nil), r.HarnessState...)
	return r
}

func (s *jsonStore) ListChats(_ context.Context, principal string, before session.ChatCursor, limit int) ([]session.ChatRecord, error) {
	out := s.records(func(r session.ChatRecord) bool {
		if r.Principal != principal {
			return false
		}
		return before.Updated.IsZero() || r.Updated.Before(before.Updated) || (r.Updated.Equal(before.Updated) && r.ID < before.ID)
	})
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// edit changes the record of a chat on a copy and keeps the copy only when it
// was saved, so a failed write leaves the chat as it was. It holds the chat's
// lock for the whole change. A change that fails leaves the chat as it was and
// nothing is saved.
func (s *jsonStore) edit(principal, id string, change func(next *chatState) error) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	next := c.chatState
	if err := change(&next); err != nil {
		return err
	}
	if err := s.save(&next); err != nil {
		if next.StateGen != c.StateGen {
			_ = os.Remove(s.stateFile(id, next.StateGen))
		}
		return err
	}
	if next.StateGen != c.StateGen && c.StateGen > 0 {
		if err := os.Remove(s.stateFile(id, c.StateGen)); err != nil && !errors.Is(err, os.ErrNotExist) {
			xlog.Warn("studio store: could not remove an old harness state file", "chat", id, "error", err)
		}
	}
	c.chatState = next
	return nil
}

// set is edit for a change that cannot fail.
func (s *jsonStore) set(principal, id string, change func(next *chatState)) error {
	return s.edit(principal, id, func(next *chatState) error { change(next); return nil })
}

// UpdateChat writes the harness state file only when the state changed: a
// title or a snapshot change does not rewrite the agent's whole history.
func (s *jsonStore) UpdateChat(_ context.Context, rec session.ChatRecord) error {
	return s.edit(rec.Principal, rec.ID, func(next *chatState) error {
		next.Rec.Title, next.Rec.Folder, next.Rec.Snapshot = rec.Title, rec.Folder, rec.Snapshot
		next.Rec.Turns, next.Rec.Updated = rec.Turns, rec.Updated
		next.Rec.Status = rec.Status
		if bytes.Equal(next.Rec.HarnessState, rec.HarnessState) {
			return nil
		}
		return s.writeState(next, rec.HarnessState)
	})
}

func (s *jsonStore) SetSkillsOff(_ context.Context, principal, id string, off []string) error {
	return s.set(principal, id, func(next *chatState) { next.Rec.SkillsOff = append([]string(nil), off...) })
}

func (s *jsonStore) SetAddonsOff(_ context.Context, principal, id string, off []string) error {
	return s.set(principal, id, func(next *chatState) { next.Rec.AddonsOff = append([]string(nil), off...) })
}

func (s *jsonStore) SetMcpOff(_ context.Context, principal, id string, off []string) error {
	return s.set(principal, id, func(next *chatState) { next.Rec.McpOff = append([]string(nil), off...) })
}

func (s *jsonStore) ListChildren(_ context.Context, principal, parentID string) ([]session.ChatRecord, error) {
	out := s.records(func(r session.ChatRecord) bool {
		return r.Principal == principal && r.ParentID == parentID && r.Kind == session.KindSubAgent
	})
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *jsonStore) SetChatMeta(_ context.Context, principal, id string, m session.ChatMeta) error {
	return s.set(principal, id, func(next *chatState) {
		if m.Title != nil {
			next.Rec.Title = *m.Title
		}
		if m.Model != nil {
			next.Rec.Model = *m.Model
		}
		if m.Settings != nil {
			next.Rec.Settings = *m.Settings
		}
	})
}

func (s *jsonStore) TruncateEntries(_ context.Context, principal, id string, keep int64) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	var kept []session.Entry
	for _, e := range c.entries {
		if e.Seq <= keep {
			kept = append(kept, e)
		}
	}
	if s.dir != "" {
		var buf bytes.Buffer
		for _, e := range kept {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			buf.Write(append(raw, '\n'))
		}
		if err := repairTranscriptCreate(s.file(id, ".entries"), buf.Bytes()); err != nil {
			return err
		}
	}
	c.entries = kept
	return nil
}

func (s *jsonStore) DeleteChat(_ context.Context, principal, id string) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	if s.dir != "" {
		files := []string{s.file(id, ".json"), s.file(id, ".entries")}
		if c.StateGen > 0 {
			files = append(files, s.stateFile(id, c.StateGen))
		}
		for _, f := range files {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	c.gone = true
	s.mu.Lock()
	delete(s.chats, id)
	s.mu.Unlock()
	return nil
}

func (s *jsonStore) AppendEntry(_ context.Context, id string, e session.Entry) (int64, error) {
	c, unlock, err := s.lock(id)
	if err != nil {
		return 0, err
	}
	defer unlock()
	// Continue after the last Seq, not the count: a skipped damaged line leaves a gap.
	e.Seq = 1
	if n := len(c.entries); n > 0 {
		e.Seq = c.entries[n-1].Seq + 1
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	e.Dur = e.Dur.Truncate(time.Millisecond) // stores keep it to the millisecond
	if s.dir != "" {
		raw, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		if err := appendLines(s.file(id, ".entries"), append(raw, '\n')); err != nil {
			return 0, err
		}
	}
	c.entries = append(c.entries, e)
	return e.Seq, nil
}

func (s *jsonStore) AppendEntries(_ context.Context, id string, entries []session.Entry) error {
	c, unlock, err := s.lock(id)
	if err != nil {
		return err
	}
	defer unlock()
	if len(entries) == 0 {
		return nil
	}
	next := int64(1)
	if n := len(c.entries); n > 0 {
		next = c.entries[n-1].Seq + 1
	}
	batch := make([]session.Entry, len(entries))
	var lines []byte
	for i, e := range entries {
		e.Seq = next + int64(i)
		if e.At.IsZero() {
			e.At = time.Now().UTC()
		}
		e.Dur = e.Dur.Truncate(time.Millisecond)
		batch[i] = e
		if s.dir != "" {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			lines = append(append(lines, raw...), '\n')
		}
	}
	if s.dir != "" {
		if err := appendLines(s.file(id, ".entries"), lines); err != nil {
			return err
		}
	}
	c.entries = append(c.entries, batch...)
	return nil
}

func (s *jsonStore) Entries(_ context.Context, id string, after int64, limit int) ([]session.Entry, error) {
	c, unlock, err := s.lock(id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var out []session.Entry
	for _, e := range c.entries {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *jsonStore) AddPending(_ context.Context, principal, id, text string) (Pending, error) {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return Pending{}, err
	}
	defer unlock()
	next := c.chatState
	next.NextPending++
	p := Pending{ID: fmt.Sprintf("p%d", next.NextPending), Text: text, Seq: next.NextPending, At: time.Now().UTC()}
	next.Pending = append(append([]Pending(nil), c.Pending...), p)
	if err := s.save(&next); err != nil {
		return Pending{}, err
	}
	c.NextPending, c.Pending = next.NextPending, next.Pending
	return p, nil
}

// undelivered returns the messages not yet delivered, in send order.
func undelivered(ps []Pending) []Pending {
	var out []Pending
	for _, p := range ps {
		if !p.Delivered {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func (s *jsonStore) Pending(_ context.Context, principal, id string) ([]Pending, error) {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return undelivered(c.Pending), nil
}

func indexOf(ps []Pending, id string) int {
	for i := range ps {
		if ps[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *jsonStore) EditPending(_ context.Context, principal, id, pendingID, text string) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	i := indexOf(c.Pending, pendingID)
	if i < 0 {
		return session.ErrNotFound
	}
	if c.Pending[i].Delivered {
		return ErrDelivered
	}
	next := c.chatState
	next.Pending = append([]Pending(nil), c.Pending...)
	next.Pending[i].Text = text
	if err := s.save(&next); err != nil {
		return err
	}
	c.Pending = next.Pending
	return nil
}

func (s *jsonStore) CancelPending(_ context.Context, principal, id, pendingID string) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	i := indexOf(c.Pending, pendingID)
	if i < 0 {
		return session.ErrNotFound
	}
	if c.Pending[i].Delivered {
		return ErrDelivered
	}
	next := c.chatState
	next.Pending = append(append([]Pending(nil), c.Pending[:i]...), c.Pending[i+1:]...)
	if err := s.save(&next); err != nil {
		return err
	}
	c.Pending = next.Pending
	return nil
}

func (s *jsonStore) TakePending(_ context.Context, principal, id string) ([]Pending, error) {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	taken := undelivered(c.Pending)
	if len(taken) == 0 {
		return nil, nil
	}
	next := c.chatState
	next.Pending = append([]Pending(nil), c.Pending...)
	for i := range next.Pending {
		if !next.Pending[i].Delivered {
			next.Pending[i].Delivered, next.Pending[i].Unconfirmed = true, true
		}
	}
	if err := s.save(&next); err != nil {
		return nil, err
	}
	c.Pending = next.Pending
	for i := range taken {
		taken[i].Delivered, taken[i].Unconfirmed = true, true
	}
	return taken, nil
}

func (s *jsonStore) ConfirmPending(_ context.Context, principal, id string, ids []string) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	next := c.chatState
	next.Pending = append([]Pending(nil), c.Pending...)
	for _, pid := range ids {
		if i := indexOf(next.Pending, pid); i >= 0 {
			next.Pending[i].Unconfirmed = false
		}
	}
	if err := s.save(&next); err != nil {
		return err
	}
	c.Pending = next.Pending
	return nil
}

func (s *jsonStore) RestorePending(_ context.Context, principal, id string, ids []string) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	next := c.chatState
	next.Pending = append([]Pending(nil), c.Pending...)
	for _, pid := range ids {
		if i := indexOf(next.Pending, pid); i >= 0 {
			next.Pending[i].Delivered, next.Pending[i].Unconfirmed = false, false
		}
	}
	if err := s.save(&next); err != nil {
		return err
	}
	c.Pending = next.Pending
	return nil
}

func (s *jsonStore) ClaimTurn(_ context.Context, principal, id, runID string) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	if c.Run != "" {
		return ErrTurnRunning
	}
	next := c.chatState
	next.Run, next.RunSince = runID, time.Now().UTC()
	next.RunBeat = next.RunSince
	if err := s.save(&next); err != nil {
		return err
	}
	c.Run, c.RunSince, c.RunBeat = next.Run, next.RunSince, next.RunBeat
	return nil
}

func (s *jsonStore) TouchTurn(_ context.Context, principal, id, runID string) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	if c.Run == "" || c.Run != runID {
		return ErrNotClaimed
	}
	// The beat is not persisted: a restart interrupts every running turn anyway.
	c.RunBeat = time.Now().UTC()
	return nil
}

func (s *jsonStore) ReapTurns(_ context.Context, olderThan time.Time) ([]ReapedTurn, error) {
	var out []ReapedTurn
	for _, c := range s.slots() {
		reaped, err := s.reapTurn(c, olderThan)
		if reaped != nil {
			out = append(out, *reaped)
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// reapTurn ends the turn of c when it has not beaten since olderThan.
func (s *jsonStore) reapTurn(c *chatSlot, olderThan time.Time) (*ReapedTurn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	beat := c.RunBeat
	if beat.IsZero() {
		beat = c.RunSince
	}
	if c.gone || c.Run == "" || !beat.Before(olderThan) {
		return nil, nil
	}
	next := c.chatState
	next.Run, next.RunSince, next.RunBeat, next.Last = "", time.Time{}, time.Time{}, TurnInterrupted
	// What a request took and nobody confirmed was lost with the frontend.
	next.Pending = append([]Pending(nil), c.Pending...)
	for i := range next.Pending {
		if next.Pending[i].Unconfirmed {
			next.Pending[i].Delivered, next.Pending[i].Unconfirmed = false, false
		}
	}
	if err := s.save(&next); err != nil {
		return nil, err
	}
	reaped := &ReapedTurn{Principal: c.Rec.Principal, ChatID: c.Rec.ID, RunID: c.Run}
	c.Run, c.RunSince, c.RunBeat, c.Last, c.Pending = next.Run, next.RunSince, next.RunBeat, next.Last, next.Pending
	return reaped, nil
}

func (s *jsonStore) ReleaseTurn(_ context.Context, principal, id, runID string, status TurnStatus) error {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return err
	}
	defer unlock()
	if c.Run == "" || c.Run != runID {
		return ErrNotClaimed
	}
	next := c.chatState
	next.Run, next.RunSince, next.RunBeat, next.Last = "", time.Time{}, time.Time{}, status
	if err := s.save(&next); err != nil {
		return err
	}
	c.Run, c.RunSince, c.RunBeat, c.Last = next.Run, next.RunSince, next.RunBeat, next.Last
	return nil
}

func (s *jsonStore) Turn(_ context.Context, principal, id string) (TurnInfo, error) {
	c, unlock, err := s.get(principal, id)
	if err != nil {
		return TurnInfo{}, err
	}
	defer unlock()
	return TurnInfo{Running: c.Run != "", RunID: c.Run, Since: c.RunSince, Beat: c.RunBeat, Last: c.Last}, nil
}
