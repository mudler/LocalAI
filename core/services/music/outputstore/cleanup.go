// SPDX-License-Identifier: MIT
package outputstore

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/music"
	"gorm.io/gorm"
)

// Cleanup backpressures on the existing caller stack BEFORE allocating a
// channel. Even cancellation does not release ownership or bypass capacity.
// No mutex/SQL transaction is held while waiting. Reconcile never takes slots.
func (s *Store) task(ctx context.Context, req string, ids []string, all bool, initial error) music.CleanupTask {
	s.cleanupSlots <- struct{}{}
	ch := make(chan error, 1)
	if initial != nil && !all {
		<-s.cleanupSlots
		return music.CleanupTask{AttemptError: initial, Released: ch}
	}
	e := initial
	if e == nil && all {
		e = s.revoke(ctx, req)
	}
	if e == nil {
		e = s.cleanup(ctx, req, ids, all)
	}
	if e == nil {
		s.retireResolved(ctx, req)
		ch <- nil
		<-s.cleanupSlots
		return music.CleanupTask{Released: ch}
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	key := req + "|" + strings.Join(sorted, ",")
	if all {
		key = req + "|*"
	}
	s.mu.Lock()
	var found *waiter
	for _, w := range s.waiters {
		if w.key == key {
			found = w
			break
		}
	}
	if found == nil {
		found = &waiter{request: req, ids: sorted, key: key, all: all}
		s.waiters = append(s.waiters, found)
	}
	found.channels = append(found.channels, ch)
	s.mu.Unlock()
	return music.CleanupTask{AttemptError: e, Released: ch}
}
func (s *Store) CleanInput(ctx context.Context, req string, in music.OwnedInput) music.CleanupTask {
	if in == (music.OwnedInput{}) {
		return s.task(ctx, req, nil, false, nil)
	}
	r, ok := s.tokenInput(req, in)
	if !ok {
		return s.task(ctx, req, nil, false, ErrDenied)
	}
	return s.task(ctx, req, []string{r.id}, false, nil)
}
func (s *Store) CleanArtifact(ctx context.Context, req string, c music.Candidate, p music.PreparedOutput) music.CleanupTask {
	var ids []string
	if c != (music.Candidate{}) {
		r, ok := s.tokenCandidate(req, c)
		if !ok {
			return s.task(ctx, req, nil, false, ErrDenied)
		}
		ids = append(ids, r.id)
	}
	if p != (music.PreparedOutput{}) {
		r, ok := s.tokenPrepared(req, p)
		if !ok {
			return s.task(ctx, req, nil, false, ErrDenied)
		}
		ids = append(ids, r.id)
	}
	return s.task(ctx, req, ids, false, nil)
}
func (s *Store) Revoke(ctx context.Context, req string) music.CleanupTask {
	return s.task(ctx, req, nil, true, nil)
}

func (s *Store) cleanup(ctx context.Context, req string, ids []string, all bool) error {
	if !all && len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := s.lockRequest(tx, req, false)
		if e != nil {
			return e
		}
		q := tx.Where("request_id = ? AND state NOT IN ?", req, []string{"deleted", "reclaimed"}).Where("NOT (state = ? AND expires_at > ?)", "committed", s.clock().UTC())
		if !all {
			q = q.Where("id IN ?", ids)
		}
		var rows []artifactRow
		if e = q.Order("id").Limit(reconcileBatch).Find(&rows).Error; e != nil {
			return e
		}
		for _, a := range rows {
			if a.State == "committed" && a.ExpiresAt.After(s.clock().UTC()) {
				continue
			}
			// An active prepared output can only be discarded after durable request
			// revocation; cleaning a local candidate cannot accidentally publish it.
			if a.Kind == "object" && r.State == "active" {
				if e = tx.Model(&requestRow{}).Where("id = ?", req).Update("state", "revoked").Error; e != nil {
					return e
				}
				r.State = "revoked"
			}
			if e = s.deleteArtifact(ctx, tx, &a); e != nil {
				return e
			}
		}
		var remaining artifactRow
		e = tx.Where("request_id = ? AND state NOT IN ?", req, []string{"deleted", "reclaimed"}).Where("NOT (state = ? AND expires_at > ?)", "committed", s.clock().UTC()).Limit(1).Take(&remaining).Error
		if !all {
			return nil
		}
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		return errors.New("music cleanup page incomplete")
	})
}
func (s *Store) deleteArtifact(ctx context.Context, tx *gorm.DB, a *artifactRow) error {
	if a.Kind == "object" {
		if e := tx.Where("object_key = ?", a.ObjectKey).Delete(&Manifest{}).Error; e != nil {
			return e
		}
		// Generation changes and lease ownership persist on successful cleanup.
		// Retain deletion tombstone for late remote completion after process death.
		if e := s.objects.Delete(ctx, a.ObjectKey); e != nil {
			return e
		}
		a.State = "reclaimed"
		a.Generation++
		a.LeaseOwner = s.instance
		a.LeaseUntil = s.clock().UTC()
	} else {
		if a.State == "deleted" {
			return nil
		}
		if a.StagingOwner == "" || a.StagingOwner != s.stagingOwner {
			return errors.New("music staging cleanup belongs to another namespace")
		}
		if e := s.root.Remove(a.Leaf); e != nil && !os.IsNotExist(e) {
			return e
		}
		dir := strings.Split(a.Leaf, "/")[0]
		if e := s.root.Remove(dir); e != nil && !os.IsNotExist(e) {
			return e
		}
		a.State = "deleted"
		a.Generation++
	}
	return tx.Save(a).Error
}
func (s *Store) cleanCommittedLocals(ctx context.Context, req string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := s.lockRequest(tx, req, false); e != nil {
			return e
		}
		var rows []artifactRow
		if e := tx.Where("request_id = ? AND kind <> ? AND state <> ?", req, "object", "deleted").Order("id").Limit(reconcileBatch).Find(&rows).Error; e != nil {
			return e
		}
		for i := range rows {
			if e := s.deleteArtifact(ctx, tx, &rows[i]); e != nil {
				return e
			}
		}
		return nil
	})
}

// Reconcile performs no inference and no fetch. Reclaimed object tombstones are
// deliberately re-deleted indefinitely: ObjectStore has no remote PUT fencing
// primitive, so one successful DELETE after a crash cannot prove no late PUT.
// Applications must schedule this method; it does not start background loops.
const reconcileBatch = 64

func (s *Store) Reconcile(ctx context.Context) error {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	// Independent keyset cursors ensure requests with many artifacts cannot
	// starve later requests. Each invocation reads at most one page per table.
	var requests []requestRow
	if e := s.db.WithContext(ctx).Where("id > ?", s.requestCursor).Order("id").Limit(reconcileBatch).Find(&requests).Error; e != nil {
		return e
	}
	var errs error
	for _, r := range requests {
		e := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			locked, e := s.lockRequest(tx, r.ID, false)
			if e != nil {
				return e
			}
			if locked.State == "active" && !locked.Deadline.After(s.clock().UTC()) {
				return tx.Model(&requestRow{}).Where("id = ?", r.ID).Update("state", "revoked").Error
			}
			return nil
		})
		if e == nil {
			s.retireResolved(ctx, r.ID)
		}
		errs = errors.Join(errs, e)
		s.requestCursor = r.ID
	}
	if len(requests) < reconcileBatch {
		s.requestCursor = ""
	}
	var rows []artifactRow
	if e := s.db.WithContext(ctx).Where("id > ?", s.artifactCursor).Order("id").Limit(reconcileBatch).Find(&rows).Error; e != nil {
		return errors.Join(errs, e)
	}
	for _, snapshot := range rows {
		e := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			locked, e := s.lockRequest(tx, snapshot.RequestID, false)
			if e != nil {
				return e
			}
			var a artifactRow
			if e = tx.First(&a, "id = ?", snapshot.ID).Error; e != nil {
				return e
			}
			now := s.clock().UTC()
			eligible := a.State == "reclaimed" || a.State == "deleted" || !a.ExpiresAt.After(now) || locked.State == "revoked" || (locked.State == "committed" && a.Kind != "object")
			if a.Kind == "object" && a.State == "pending" && !a.LeaseUntil.After(now) {
				eligible = true
			}
			if a.State == "committed" && a.ExpiresAt.After(now) {
				eligible = false
			}
			if eligible {
				return s.deleteArtifact(ctx, tx, &a)
			}
			return nil
		})
		if e == nil {
			s.retireResolved(ctx, snapshot.RequestID)
		}
		errs = errors.Join(errs, e)
		s.artifactCursor = snapshot.ID
	}
	if len(rows) < reconcileBatch {
		s.artifactCursor = ""
	}

	s.mu.Lock()
	n := len(s.waiters)
	if n > 8 {
		n = 8
	}
	pending := append([]*waiter(nil), s.waiters[:n]...)
	// Keep records visible so concurrent duplicate callers join the same record.
	s.mu.Unlock()
	for _, w := range pending {
		var e error
		if w.all {
			e = s.revoke(ctx, w.request)
		}
		if e == nil {
			e = s.cleanup(ctx, w.request, w.ids, w.all)
		}
		if e == nil {
			s.retireResolved(ctx, w.request)
			s.mu.Lock()
			for i, current := range s.waiters {
				if current == w {
					s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
					break
				}
			}
			for _, ch := range w.channels {
				ch <- nil
				<-s.cleanupSlots
			}
			s.mu.Unlock()
		} else {
			errs = errors.Join(errs, e)
			s.mu.Lock()
			for i, current := range s.waiters {
				if current == w {
					s.waiters = append(append(s.waiters[:i], s.waiters[i+1:]...), w)
					break
				}
			}
			s.mu.Unlock()
		}
	}

	return errs
}

// retireResolved drops only adapter indexes whose durable resource is terminal.
// Issuer token retirement remains Runner's responsibility under the frozen API.
func (s *Store) retireResolved(ctx context.Context, req string) {
	s.mu.Lock()
	refs := map[string]bool{}
	for _, r := range s.inputs {
		if r.request == req {
			refs[r.id] = true
		}
	}
	for _, r := range s.candidates {
		if r.request == req {
			refs[r.id] = true
		}
	}
	for _, r := range s.prepared {
		if r.request == req {
			refs[r.id] = true
		}
	}
	s.mu.Unlock()
	for id := range refs {
		var row artifactRow
		if s.db.WithContext(ctx).First(&row, "id = ?", id).Error != nil {
			continue
		}
		if row.State != "deleted" && row.State != "reclaimed" && row.State != "committed" {
			continue
		}
		s.mu.Lock()
		for k, r := range s.inputs {
			if r.id == id {
				delete(s.inputs, k)
			}
		}
		for k, r := range s.candidates {
			if r.id == id {
				delete(s.candidates, k)
			}
		}
		for k, r := range s.prepared {
			if r.id == id {
				delete(s.prepared, k)
			}
		}
		s.mu.Unlock()
	}
	s.releaseAdmission(ctx, req)
}

func (s *Store) releaseAdmission(ctx context.Context, req string) {
	var r requestRow
	if s.db.WithContext(ctx).First(&r, "id = ?", req).Error != nil || r.State == "active" {
		return
	}
	// Exact terminal-state check is insufficient: failed allocations may own
	// bytes without ever issuing a capability. Probe durable ownership as well.
	var outstanding artifactRow
	err := s.db.WithContext(ctx).Where("request_id = ? AND state NOT IN ?", req, []string{"deleted", "reclaimed", "committed"}).Limit(1).Take(&outstanding).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.inputs {
		if v.request == req {
			return
		}
	}
	for _, v := range s.candidates {
		if v.request == req {
			return
		}
	}
	for _, v := range s.prepared {
		if v.request == req {
			return
		}
	}
	delete(s.admitted, req)
}
