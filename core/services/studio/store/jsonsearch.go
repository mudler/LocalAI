package store

import (
	"context"
	"sort"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/chatsearch"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// SearchText implements chatsearch.Index. The transcripts are in memory, so it
// scans them, the newest chat first, until chatsearch.ScanBudget is spent.
// Result.Searched and Total say how many chats it read.
func (s *jsonStore) SearchText(ctx context.Context, q chatsearch.Query) (chatsearch.Result, error) {
	res := chatsearch.Result{Engine: engineScan}
	terms := q.Terms()
	if len(terms) == 0 {
		return res, nil
	}
	type listed struct {
		slot    *chatSlot
		updated time.Time
		id      string
	}
	var chats []listed
	for _, c := range s.slots() {
		c.mu.Lock()
		if !c.gone && q.ChatMatches(c.Rec) {
			chats = append(chats, listed{c, c.Rec.Updated, c.Rec.ID})
		}
		c.mu.Unlock()
	}
	sort.Slice(chats, func(i, j int) bool {
		if !chats[i].updated.Equal(chats[j].updated) {
			return chats[i].updated.After(chats[j].updated)
		}
		return chats[i].id > chats[j].id
	})
	res.Total = len(chats)

	var (
		cands  []chatsearch.Candidate
		titles []session.ChatRecord
	)
	deadline := time.Now().Add(chatsearch.ScanBudget)
	for _, l := range chats {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if time.Now().After(deadline) {
			res.Partial = true
			break
		}
		c := l.slot
		c.mu.Lock()
		if c.gone {
			c.mu.Unlock()
			continue
		}
		rec := cloneRec(c.Rec)
		rec.HarnessState = nil
		if q.Titles && chatsearch.Matches(chatsearch.Fold(rec.Title), terms) {
			titles = append(titles, rec)
		}
		for _, e := range c.entries {
			if q.Role != "" && e.Role != q.Role || !q.TimeMatches(e.At) {
				continue
			}
			display, ok := chatsearch.IndexText(e.Role, e.Text, len(e.Attachments) > 0)
			if ok && chatsearch.Matches(chatsearch.Fold(display), terms) {
				cands = append(cands, chatsearch.Candidate{Chat: rec, Seq: e.Seq, Role: e.Role, Text: display, At: e.At})
			}
		}
		c.mu.Unlock()
		res.Searched++
	}
	// The pool is the newest messages, as in an indexed store.
	if len(cands) > chatsearch.PoolSize {
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].At.After(cands[j].At) })
		cands = cands[:chatsearch.PoolSize]
	}
	res.Hits = chatsearch.Build(q, cands, titles)
	return res, nil
}

var _ chatsearch.Index = (*jsonStore)(nil)
