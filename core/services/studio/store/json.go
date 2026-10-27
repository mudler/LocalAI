package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/xlog"
)

// jsonStore keeps chats in memory and, when it has a directory, mirrors them to
// files: one small record file per chat (rewritten atomically) and one
// append-only transcript file per chat. One process owns the directory: the
// lock below is in-process only, so two LocalAI processes must not share it.
// A cluster uses the GORM store.
type jsonStore struct {
	mu    sync.Mutex
	dir   string // empty: memory only
	chats map[string]*chatState
}

type chatState struct {
	Rec         session.ChatRecord `json:"rec"`
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
	s := &jsonStore{dir: dir, chats: map[string]*chatState{}}
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
		s.chats[c.Rec.ID] = &c
	}
	return s, nil
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

// save rewrites the record file of c atomically.
func (s *jsonStore) save(c *chatState) error {
	if s.dir == "" {
		return nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.chatDir(), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(raw)
	// Without the sync a power loss after the rename can leave an empty record.
	serr := tmp.Sync()
	cerr := tmp.Close()
	if werr != nil || serr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(werr, serr, cerr)
	}
	return os.Rename(tmp.Name(), s.file(c.Rec.ID, ".json"))
}

// get returns the chat when principal owns it. The caller holds s.mu.
func (s *jsonStore) get(principal, id string) (*chatState, error) {
	c, ok := s.chats[id]
	if !ok || c.Rec.Principal != principal {
		return nil, session.ErrNotFound
	}
	return c, nil
}

func (s *jsonStore) CreateChat(_ context.Context, rec session.ChatRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.chats[rec.ID]; ok {
		return session.ErrExists
	}
	c := &chatState{Rec: rec}
	if err := s.save(c); err != nil {
		return err
	}
	s.chats[rec.ID] = c
	return nil
}

func (s *jsonStore) GetChat(_ context.Context, principal, id string) (session.ChatRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return session.ChatRecord{}, err
	}
	return cloneRec(c.Rec), nil
}

func cloneRec(r session.ChatRecord) session.ChatRecord {
	r.HarnessState = append([]byte(nil), r.HarnessState...)
	return r
}

func (s *jsonStore) ListChats(_ context.Context, principal string, before session.ChatCursor, limit int) ([]session.ChatRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []session.ChatRecord
	for _, c := range s.chats {
		r := c.Rec
		if r.Principal != principal {
			continue
		}
		if !before.Updated.IsZero() {
			older := r.Updated.Before(before.Updated) || (r.Updated.Equal(before.Updated) && r.ID < before.ID)
			if !older {
				continue
			}
		}
		out = append(out, cloneRec(r))
	}
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

func (s *jsonStore) UpdateChat(_ context.Context, rec session.ChatRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(rec.Principal, rec.ID)
	if err != nil {
		return err
	}
	next := *c
	next.Rec.Title, next.Rec.Folder, next.Rec.Snapshot = rec.Title, rec.Folder, rec.Snapshot
	next.Rec.HarnessState = append([]byte(nil), rec.HarnessState...)
	next.Rec.Turns, next.Rec.Updated = rec.Turns, rec.Updated
	next.Rec.Status = rec.Status
	if err := s.save(&next); err != nil {
		return err
	}
	c.Rec = next.Rec
	return nil
}

func (s *jsonStore) SetSkillsOff(_ context.Context, principal, id string, off []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	next := *c
	next.Rec.SkillsOff = append([]string(nil), off...)
	if err := s.save(&next); err != nil {
		return err
	}
	c.Rec.SkillsOff = next.Rec.SkillsOff
	return nil
}

func (s *jsonStore) SetAddonsOff(_ context.Context, principal, id string, off []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	next := *c
	next.Rec.AddonsOff = append([]string(nil), off...)
	if err := s.save(&next); err != nil {
		return err
	}
	c.Rec.AddonsOff = next.Rec.AddonsOff
	return nil
}

func (s *jsonStore) SetMcpOff(_ context.Context, principal, id string, off []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	next := *c
	next.Rec.McpOff = append([]string(nil), off...)
	if err := s.save(&next); err != nil {
		return err
	}
	c.Rec.McpOff = next.Rec.McpOff
	return nil
}

func (s *jsonStore) ListChildren(_ context.Context, principal, parentID string) ([]session.ChatRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []session.ChatRecord
	for _, c := range s.chats {
		if r := c.Rec; r.Principal == principal && r.ParentID == parentID && r.Kind == session.KindSubAgent {
			out = append(out, cloneRec(r))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *jsonStore) SetChatMeta(_ context.Context, principal, id string, m session.ChatMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	next := *c
	if m.Title != nil {
		next.Rec.Title = *m.Title
	}
	if m.Model != nil {
		next.Rec.Model = *m.Model
	}
	if m.Settings != nil {
		next.Rec.Settings = *m.Settings
	}
	if err := s.save(&next); err != nil {
		return err
	}
	c.Rec = next.Rec
	return nil
}

func (s *jsonStore) TruncateEntries(_ context.Context, principal, id string, keep int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.get(principal, id); err != nil {
		return err
	}
	if s.dir != "" {
		for _, ext := range []string{".json", ".entries"} {
			if err := os.Remove(s.file(id, ext)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	delete(s.chats, id)
	return nil
}

func (s *jsonStore) AppendEntry(_ context.Context, id string, e session.Entry) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[id]
	if !ok {
		return 0, session.ErrNotFound
	}
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
		f, err := os.OpenFile(s.file(id, ".entries"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return 0, err
		}
		_, werr := f.Write(append(raw, '\n'))
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return 0, werr
		}
	}
	c.entries = append(c.entries, e)
	return e.Seq, nil
}

func (s *jsonStore) AppendEntries(_ context.Context, id string, entries []session.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[id]
	if !ok {
		return session.ErrNotFound
	}
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
		f, err := os.OpenFile(s.file(id, ".entries"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, werr := f.Write(lines)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	c.entries = append(c.entries, batch...)
	return nil
}

func (s *jsonStore) Entries(_ context.Context, id string, after int64, limit int) ([]session.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[id]
	if !ok {
		return nil, session.ErrNotFound
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return Pending{}, err
	}
	next := *c
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
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return nil, err
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	i := indexOf(c.Pending, pendingID)
	if i < 0 {
		return session.ErrNotFound
	}
	if c.Pending[i].Delivered {
		return ErrDelivered
	}
	next := *c
	next.Pending = append([]Pending(nil), c.Pending...)
	next.Pending[i].Text = text
	if err := s.save(&next); err != nil {
		return err
	}
	c.Pending = next.Pending
	return nil
}

func (s *jsonStore) CancelPending(_ context.Context, principal, id, pendingID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	i := indexOf(c.Pending, pendingID)
	if i < 0 {
		return session.ErrNotFound
	}
	if c.Pending[i].Delivered {
		return ErrDelivered
	}
	next := *c
	next.Pending = append(append([]Pending(nil), c.Pending[:i]...), c.Pending[i+1:]...)
	if err := s.save(&next); err != nil {
		return err
	}
	c.Pending = next.Pending
	return nil
}

func (s *jsonStore) TakePending(_ context.Context, principal, id string) ([]Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return nil, err
	}
	taken := undelivered(c.Pending)
	if len(taken) == 0 {
		return nil, nil
	}
	next := *c
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
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	next := *c
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
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	next := *c
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
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	if c.Run != "" {
		return ErrTurnRunning
	}
	next := *c
	next.Run, next.RunSince = runID, time.Now().UTC()
	next.RunBeat = next.RunSince
	if err := s.save(&next); err != nil {
		return err
	}
	c.Run, c.RunSince, c.RunBeat = next.Run, next.RunSince, next.RunBeat
	return nil
}

func (s *jsonStore) TouchTurn(_ context.Context, principal, id, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	if c.Run == "" || c.Run != runID {
		return ErrNotClaimed
	}
	// The beat is not persisted: a restart interrupts every running turn anyway.
	c.RunBeat = time.Now().UTC()
	return nil
}

func (s *jsonStore) ReapTurns(_ context.Context, olderThan time.Time) ([]ReapedTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ReapedTurn
	for _, c := range s.chats {
		beat := c.RunBeat
		if beat.IsZero() {
			beat = c.RunSince
		}
		if c.Run == "" || !beat.Before(olderThan) {
			continue
		}
		next := *c
		next.Run, next.RunSince, next.RunBeat, next.Last = "", time.Time{}, time.Time{}, TurnInterrupted
		// What a request took and nobody confirmed was lost with the frontend.
		next.Pending = append([]Pending(nil), c.Pending...)
		for i := range next.Pending {
			if next.Pending[i].Unconfirmed {
				next.Pending[i].Delivered, next.Pending[i].Unconfirmed = false, false
			}
		}
		if err := s.save(&next); err != nil {
			return out, err
		}
		out = append(out, ReapedTurn{Principal: c.Rec.Principal, ChatID: c.Rec.ID, RunID: c.Run})
		c.Run, c.RunSince, c.RunBeat, c.Last, c.Pending = next.Run, next.RunSince, next.RunBeat, next.Last, next.Pending
	}
	return out, nil
}

func (s *jsonStore) ReleaseTurn(_ context.Context, principal, id, runID string, status TurnStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return err
	}
	if c.Run == "" || c.Run != runID {
		return ErrNotClaimed
	}
	next := *c
	next.Run, next.RunSince, next.RunBeat, next.Last = "", time.Time{}, time.Time{}, status
	if err := s.save(&next); err != nil {
		return err
	}
	c.Run, c.RunSince, c.RunBeat, c.Last = next.Run, next.RunSince, next.RunBeat, next.Last
	return nil
}

func (s *jsonStore) Turn(_ context.Context, principal, id string) (TurnInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.get(principal, id)
	if err != nil {
		return TurnInfo{}, err
	}
	return TurnInfo{Running: c.Run != "", RunID: c.Run, Since: c.RunSince, Beat: c.RunBeat, Last: c.Last}, nil
}
