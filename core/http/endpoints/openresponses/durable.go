package openresponses

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// defaultDurableTTL is the expiry applied to rows of the table store when
	// the configured TTL is 0. A memory store may live without expiry because
	// it ends with the process; a table does not, so it needs a bound.
	defaultDurableTTL = 7 * 24 * time.Hour

	// defaultLeaseEvery is how often an owner renews the lease of its running
	// responses. defaultLeaseTTL is three beats, so one missed beat is not a
	// death.
	defaultLeaseEvery = 15 * time.Second
	defaultLeaseTTL   = 45 * time.Second

	// defaultBatchEvery and defaultBatchEvents bound how long a delta waits in
	// memory before it is written. Events that are not deltas are written at
	// once.
	defaultBatchEvery  = 250 * time.Millisecond
	defaultBatchEvents = 64

	// sweepEvery is how often the sweeper looks for dead runs and expired rows.
	sweepEvery = 15 * time.Second

	// sweepBatch bounds the rows one expiry pass deletes.
	sweepBatch = 500

	// pollEvery is how often a reader that does not own a response looks for
	// new events in the table.
	pollEvery = 250 * time.Millisecond

	// ErrorCodeServerRestarted is the error code of a response whose owner died
	// before it finished.
	ErrorCodeServerRestarted = "server_restarted"
)

var (
	nonTerminalStatuses = []string{schema.ORStatusQueued, schema.ORStatusInProgress}
	terminalStatuses    = []string{schema.ORStatusCompleted, schema.ORStatusFailed, schema.ORStatusIncomplete, schema.ORStatusCancelled}
)

func isNonTerminal(status string) bool {
	return status == schema.ORStatusQueued || status == schema.ORStatusInProgress
}

// responseRow is the table form of a stored response. Times are unix
// milliseconds (created_at and expires_at: seconds are enough, but one unit
// keeps the comparisons the same on PostgreSQL and SQLite, which stores times
// as text). LeaseUntil is 0 for a row that has no lease.
type responseRow struct {
	ID              string `gorm:"primaryKey;size:191"`
	Owner           string `gorm:"index;size:191"`
	Status          string `gorm:"index;size:32"`
	Request         string `gorm:"type:text"`
	Response        string `gorm:"type:text"`
	Background      bool
	Stream          bool
	OwnerInstance   string `gorm:"size:191"`
	LeaseUntil      int64  `gorm:"index"`
	CancelRequested bool
	LastSeq         int
	DroppedThrough  int
	CreatedAt       int64 `gorm:"column:created_at;autoCreateTime:false"`
	ExpiresAt       int64 `gorm:"index"`
}

func (responseRow) TableName() string { return "open_responses" }

// responseEventRow is one buffered stream event.
type responseEventRow struct {
	ResponseID string `gorm:"primaryKey;size:191"`
	Seq        int    `gorm:"primaryKey;autoIncrement:false"`
	EventType  string `gorm:"size:96"`
	Data       []byte
	Bytes      int
}

func (responseEventRow) TableName() string { return "open_response_events" }

// DurableOptions tunes the table store. Zero values take the defaults.
type DurableOptions struct {
	// Token names this process in the owner_instance column and in the lease.
	// It must be unique per live process. Empty: a random one.
	Token string
	// LeaseEvery and LeaseTTL set the lease renewal and its length.
	LeaseEvery time.Duration
	LeaseTTL   time.Duration
	// BatchEvery and BatchEvents bound how long deltas wait before a write.
	BatchEvery  time.Duration
	BatchEvents int
}

// durableBackend is the table side of a ResponseStore. The ResponseStore keeps
// its map as the owner's cache and as the live notification path; this is the
// record every replica reads and that outlives the process.
type durableBackend struct {
	db          *gorm.DB
	token       string
	leaseEvery  time.Duration
	leaseTTL    time.Duration
	batchEvery  time.Duration
	batchEvents int
}

func newDurableBackend(db *gorm.DB, opts DurableOptions) (*durableBackend, error) {
	if db == nil {
		return nil, errors.New("open responses store: nil database")
	}
	d := &durableBackend{
		db:          db,
		token:       opts.Token,
		leaseEvery:  opts.LeaseEvery,
		leaseTTL:    opts.LeaseTTL,
		batchEvery:  opts.BatchEvery,
		batchEvents: opts.BatchEvents,
	}
	if d.token == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		d.token = "proc-" + hex.EncodeToString(b[:])
	}
	if d.leaseEvery <= 0 {
		d.leaseEvery = defaultLeaseEvery
	}
	if d.leaseTTL <= 0 {
		d.leaseTTL = defaultLeaseTTL
	}
	if d.batchEvery <= 0 {
		d.batchEvery = defaultBatchEvery
	}
	if d.batchEvents <= 0 {
		d.batchEvents = defaultBatchEvents
	}
	// Tables first: the code that writes to them starts after this returns.
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&responseRow{}, &responseEventRow{})
	}); err != nil {
		return nil, fmt.Errorf("open responses store: migrating tables: %w", err)
	}
	return d, nil
}

func nowMs() int64 { return time.Now().UnixMilli() }

func timeMs(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixMilli()
}

func msTime(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms)
	return &t
}

// metaColumns are the columns a metadata write replaces. The lease, the cancel
// flag and the event counters belong to other writers and are left alone.
var metaColumns = []string{"owner", "status", "request", "response", "background", "stream", "owner_instance", "expires_at"}

// rowFor builds the row of a stored response. The caller must hold stored.mu
// for reading.
func (d *durableBackend) rowFor(id string, stored *StoredResponse) (responseRow, error) {
	req, err := json.Marshal(stored.Request)
	if err != nil {
		return responseRow{}, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := json.Marshal(stored.Response)
	if err != nil {
		return responseRow{}, fmt.Errorf("marshal response: %w", err)
	}
	row := responseRow{
		ID:             id,
		Owner:          stored.Owner,
		Request:        string(req),
		Response:       string(resp),
		Background:     stored.IsBackground,
		Stream:         stored.StreamEnabled,
		OwnerInstance:  d.token,
		CreatedAt:      stored.StoredAt.UnixMilli(),
		ExpiresAt:      timeMs(stored.ExpiresAt),
		DroppedThrough: -1,
	}
	if stored.Response != nil {
		row.Status = stored.Response.Status
	}
	if isNonTerminal(row.Status) {
		row.LeaseUntil = nowMs() + d.leaseTTL.Milliseconds()
	}
	return row, nil
}

// upsertMeta inserts the row or replaces its metadata columns.
func (d *durableBackend) upsertMeta(ctx context.Context, row responseRow) error {
	return d.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns(metaColumns),
	}).Create(&row).Error
}

func (d *durableBackend) getRow(ctx context.Context, id string) (*responseRow, error) {
	var row responseRow
	err := d.db.WithContext(ctx).Where("id = ?", id).Take(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// insertEvents writes a batch and the counters it changes in one transaction,
// then removes the rows the memory buffer has evicted, so the table obeys the
// same caps as the buffer.
func (d *durableBackend) insertEvents(ctx context.Context, id string, batch []StreamedEvent, droppedThrough int) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lastSeq := -1
		if len(batch) > 0 {
			rows := make([]responseEventRow, len(batch))
			for i, e := range batch {
				rows[i] = responseEventRow{ResponseID: id, Seq: e.SequenceNumber, EventType: e.EventType, Data: e.Data, Bytes: len(e.Data)}
				if e.SequenceNumber > lastSeq {
					lastSeq = e.SequenceNumber
				}
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "response_id"}, {Name: "seq"}},
				DoUpdates: clause.AssignmentColumns([]string{"event_type", "data", "bytes"}),
			}).CreateInBatches(rows, 100).Error; err != nil {
				return err
			}
			if err := tx.Model(&responseRow{}).Where("id = ? AND last_seq < ?", id, lastSeq).Update("last_seq", lastSeq).Error; err != nil {
				return err
			}
		}
		if droppedThrough >= 0 {
			if err := tx.Where("response_id = ? AND seq <= ?", id, droppedThrough).Delete(&responseEventRow{}).Error; err != nil {
				return err
			}
			if err := tx.Model(&responseRow{}).Where("id = ? AND dropped_through < ?", id, droppedThrough).Update("dropped_through", droppedThrough).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// eventsAfter reads the events with a sequence above startingAfter.
func (d *durableBackend) eventsAfter(ctx context.Context, id string, startingAfter int) ([]StreamedEvent, error) {
	row, err := d.getRow(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("response not found: %s", id)
		}
		return nil, err
	}
	if startingAfter < row.DroppedThrough {
		return nil, ErrOffsetLost
	}
	var rows []responseEventRow
	if err := d.db.WithContext(ctx).Where("response_id = ? AND seq > ?", id, startingAfter).Order("seq ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var out []StreamedEvent
	for _, r := range rows {
		out = append(out, StreamedEvent{SequenceNumber: r.Seq, EventType: r.EventType, Data: r.Data})
	}
	return out, nil
}

func (d *durableBackend) clearEvents(ctx context.Context, id string) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("response_id = ?", id).Delete(&responseEventRow{}).Error; err != nil {
			return err
		}
		return tx.Model(&responseRow{}).Where("id = ?", id).Update("dropped_through", -1).Error
	})
}

func (d *durableBackend) deleteResponse(ctx context.Context, id string) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("response_id = ?", id).Delete(&responseEventRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&responseRow{}).Error
	})
}

// renewLeases extends the lease of the given responses, only while they are
// running and still ours.
func (d *durableBackend) renewLeases(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return d.db.WithContext(ctx).Model(&responseRow{}).
		Where("id IN ? AND owner_instance = ? AND status IN ?", ids, d.token, nonTerminalStatuses).
		Update("lease_until", nowMs()+d.leaseTTL.Milliseconds()).Error
}

type rowState struct {
	ID              string
	Status          string
	CancelRequested bool
}

func (d *durableBackend) states(ctx context.Context, ids []string) ([]rowState, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []rowState
	err := d.db.WithContext(ctx).Model(&responseRow{}).Select("id", "status", "cancel_requested").Where("id IN ?", ids).Find(&out).Error
	return out, err
}

// requestCancel flags a running response, so its owner stops it on the next
// lease tick even if the bus message is lost. It reports whether a running row
// was flagged.
func (d *durableBackend) requestCancel(ctx context.Context, id string) (bool, error) {
	res := d.db.WithContext(ctx).Model(&responseRow{}).
		Where("id = ? AND status IN ?", id, nonTerminalStatuses).
		Update("cancel_requested", true)
	return res.RowsAffected > 0, res.Error
}

// rowToStored materialises a read-only view of a row. Remote is true, since
// this process holds no CancelFunc and no live channel for it.
func rowToStored(row *responseRow) (*StoredResponse, error) {
	stored := &StoredResponse{
		StoredAt:       time.UnixMilli(row.CreatedAt),
		ExpiresAt:      msTime(row.ExpiresAt),
		Owner:          row.Owner,
		StreamEnabled:  row.Stream,
		IsBackground:   row.Background,
		Remote:         true,
		OwnerReplica:   row.OwnerInstance,
		droppedThrough: row.DroppedThrough,
	}
	if row.Request != "" && row.Request != "null" {
		var req schema.OpenResponsesRequest
		if err := json.Unmarshal([]byte(row.Request), &req); err != nil {
			return nil, fmt.Errorf("decode request of %s: %w", row.ID, err)
		}
		stored.Request = &req
	}
	var resp schema.ORResponseResource
	if err := json.Unmarshal([]byte(row.Response), &resp); err != nil {
		return nil, fmt.Errorf("decode response of %s: %w", row.ID, err)
	}
	stored.Response = &resp
	stored.Items = make(map[string]*schema.ORItemField)
	for i := range resp.Output {
		if resp.Output[i].ID != "" {
			stored.Items[resp.Output[i].ID] = &resp.Output[i]
		}
	}
	return stored, nil
}

// findItem looks for an item ID in the stored outputs. The ID is random, so a
// substring match narrows the rows and the decode confirms the hit.
// findItem looks for an item in the rows that callerID may read (the
// accessAllowed rule, in the query, so other users' rows neither match nor take
// a place under the limit). The id is matched as text: its LIKE wildcards are
// escaped.
func (d *durableBackend) findItem(ctx context.Context, itemID, callerID string) (*schema.ORItemField, string, bool) {
	var rows []responseRow
	needle := "%" + likeEscaper.Replace("\"id\":"+jsonString(itemID)) + "%"
	if err := d.db.WithContext(ctx).
		Where("response LIKE ? ESCAPE '\\' AND (expires_at = 0 OR expires_at > ?) AND (owner = '' OR owner = ?)", needle, nowMs(), callerID).
		Limit(20).Find(&rows).Error; err != nil {
		return nil, "", false
	}
	for i := range rows {
		stored, err := rowToStored(&rows[i])
		if err != nil {
			continue
		}
		if item, ok := stored.Items[itemID]; ok {
			return item, rows[i].ID, true
		}
	}
	return nil, "", false
}

// likeEscaper escapes the LIKE wildcards and the escape character itself.
var likeEscaper = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// sweepDead ends the running responses whose owner is gone. A row is dead when
// its lease ran out. With orphansOf set (a standalone start, where one process
// owns the database) every running row of another process is dead at once.
func (d *durableBackend) sweepDead(ctx context.Context, orphansOf string) (int, error) {
	q := d.db.WithContext(ctx).Model(&responseRow{}).Where("status IN ?", nonTerminalStatuses)
	if orphansOf != "" {
		q = q.Where("owner_instance <> ?", orphansOf)
	} else {
		q = q.Where("lease_until < ?", nowMs())
	}
	var ids []string
	if err := q.Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	ended := 0
	for _, id := range ids {
		ok, err := d.endDead(ctx, id, orphansOf)
		if err != nil {
			xlog.Warn("open responses: could not end a dead response", "response_id", id, "error", err)
			continue
		}
		if ok {
			ended++
		}
	}
	return ended, nil
}

// endDead moves one dead row to its end state. The update is conditional on the
// state that was read, so two sweepers, or a sweeper and a late renewal, cannot
// both win.
func (d *durableBackend) endDead(ctx context.Context, id, orphansOf string) (bool, error) {
	won := false
	err := d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row responseRow
		if err := tx.Where("id = ?", id).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if !isNonTerminal(row.Status) {
			return nil
		}
		var resp schema.ORResponseResource
		if err := json.Unmarshal([]byte(row.Response), &resp); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		now := time.Now().Unix()
		resp.CompletedAt = &now
		status := schema.ORStatusFailed
		var apiErr *schema.ORError
		if row.CancelRequested {
			// The client asked to stop it; the owner died before it did.
			status = schema.ORStatusCancelled
		} else {
			apiErr = &schema.ORError{Type: "server_error", Code: ErrorCodeServerRestarted, Message: "The server restarted while this response was running. Send the request again."}
		}
		resp.Status = status
		resp.Error = apiErr
		body, err := json.Marshal(&resp)
		if err != nil {
			return err
		}

		cond := tx.Model(&responseRow{}).Where("id = ? AND status = ?", id, row.Status)
		if orphansOf != "" {
			cond = cond.Where("owner_instance <> ?", orphansOf)
		} else {
			cond = cond.Where("lease_until < ?", nowMs())
		}
		res := cond.Updates(map[string]any{"status": status, "response": string(body), "lease_until": 0})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		won = true

		if status == schema.ORStatusFailed {
			// A client that tails the response ends on this event.
			var maxSeq *int
			if err := tx.Model(&responseEventRow{}).Select("MAX(seq)").Where("response_id = ?", id).Scan(&maxSeq).Error; err != nil {
				return err
			}
			seq := row.LastSeq
			if maxSeq != nil && *maxSeq > seq {
				seq = *maxSeq
			}
			if row.DroppedThrough > seq {
				seq = row.DroppedThrough
			}
			seq++
			ev := schema.ORStreamEvent{
				Type:           "response.failed",
				SequenceNumber: seq,
				Response:       &resp,
				Error:          &schema.ORErrorPayload{Type: apiErr.Type, Code: apiErr.Code, Message: apiErr.Message},
			}
			data, err := json.Marshal(&ev)
			if err != nil {
				return err
			}
			if err := tx.Create(&responseEventRow{ResponseID: id, Seq: seq, EventType: ev.Type, Data: data, Bytes: len(data)}).Error; err != nil {
				return err
			}
			return tx.Model(&responseRow{}).Where("id = ?", id).Update("last_seq", seq).Error
		}
		return nil
	})
	return won && err == nil, err
}

// deleteExpired removes finished rows past their expiry, with their events.
func (d *durableBackend) deleteExpired(ctx context.Context) (int, error) {
	var ids []string
	err := d.db.WithContext(ctx).Model(&responseRow{}).
		Where("expires_at > 0 AND expires_at < ? AND status IN ?", nowMs(), terminalStatuses).
		Limit(sweepBatch).Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	err = d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("response_id IN ?", ids).Delete(&responseEventRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Delete(&responseRow{}).Error
	})
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

// ---- ResponseStore side ----

// EnableDurable puts the tables behind the store. Without a database a store
// stays what it was: a process-local map. Call it once at start, before the
// store serves a request.
func (s *ResponseStore) EnableDurable(db *gorm.DB, opts DurableOptions) error {
	d, err := newDurableBackend(db, opts)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.dur = d
	s.restartCleanupLocked()
	s.mu.Unlock()
	return nil
}

// Durable reports whether the tables are behind the store.
func (s *ResponseStore) Durable() bool { return s.durable() != nil }

func (s *ResponseStore) durable() *durableBackend {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dur
}

// persist writes the metadata of a response to its row. A failed write is
// logged and the response goes on from memory: the client that started it is
// still served, and the next write retries.
func (s *ResponseStore) persist(id string, stored *StoredResponse) {
	d := s.durable()
	if d == nil || s.metadataViaSync() {
		return
	}
	stored.mu.RLock()
	if stored.fenced {
		stored.mu.RUnlock()
		return
	}
	terminal := stored.Response != nil && !isNonTerminal(stored.Response.Status)
	row, err := d.rowFor(id, stored)
	stored.mu.RUnlock()
	if err != nil {
		xlog.Warn("open responses: could not encode a response", "response_id", id, "error", err)
		return
	}
	if terminal {
		// The events come first: a reader that sees the end state must find
		// every event.
		s.flushEvents(id, stored)
	}
	if err := d.upsertMeta(context.Background(), row); err != nil {
		xlog.Warn("open responses: could not write a response", "response_id", id, "error", err)
	}
}

// isDeltaEvent reports whether an event is one of the many small text events
// that are worth batching.
func isDeltaEvent(eventType string) bool { return strings.HasSuffix(eventType, ".delta") }

// queueEvent adds an event to the batch of a response and decides if the batch
// is written now. It is called with stored.mu held and returns whether the
// caller must flush after releasing it.
func (d *durableBackend) queueEvent(stored *StoredResponse, ev StreamedEvent) (flushNow bool) {
	stored.pending = append(stored.pending, ev)
	if !isDeltaEvent(ev.EventType) || len(stored.pending) >= d.batchEvents {
		return true
	}
	return time.Since(stored.lastFlush) >= d.batchEvery
}

// armFlushTimer makes sure a batch that waits is written within batchEvery. It
// is called with stored.mu held.
func (s *ResponseStore) armFlushTimer(d *durableBackend, id string, stored *StoredResponse) {
	if stored.flushTimer != nil || len(stored.pending) == 0 {
		return
	}
	stored.flushTimer = time.AfterFunc(d.batchEvery, func() {
		stored.mu.Lock()
		stored.flushTimer = nil
		stored.mu.Unlock()
		s.flushEvents(id, stored)
	})
}

// flushEvents writes the waiting events of a response. Flushes of one response
// are serialised, so the rows go to the table in sequence order.
func (s *ResponseStore) flushEvents(id string, stored *StoredResponse) {
	d := s.durable()
	if d == nil {
		return
	}
	stored.flushMu.Lock()
	defer stored.flushMu.Unlock()

	stored.mu.Lock()
	batch := stored.pending
	stored.pending = nil
	dropped := stored.droppedThrough
	fenced := stored.fenced
	stored.lastFlush = time.Now()
	if stored.flushTimer != nil {
		stored.flushTimer.Stop()
		stored.flushTimer = nil
	}
	stored.mu.Unlock()
	if fenced || (len(batch) == 0 && dropped < 0) {
		return
	}

	err := d.insertEvents(context.Background(), id, batch, dropped)
	stored.mu.Lock()
	defer stored.mu.Unlock()
	if err == nil {
		stored.flushFailedSince = time.Time{}
		return
	}
	xlog.Warn("open responses: could not write events", "response_id", id, "error", err)
	// Keep them for the next try, in order.
	stored.pending = append(batch, stored.pending...)
	if stored.flushFailedSince.IsZero() {
		stored.flushFailedSince = time.Now()
	}
	if time.Since(stored.flushFailedSince) > d.leaseTTL && stored.Response != nil && isNonTerminal(stored.Response.Status) {
		// The lease would run out anyway, and the sweeper would end the run.
		// End it here so the client sees why.
		xlog.Error("open responses: the database stayed unavailable, ending the response", "response_id", id)
		now := time.Now().Unix()
		stored.Response.Status = schema.ORStatusFailed
		stored.Response.CompletedAt = &now
		stored.Response.Error = &schema.ORError{Type: "server_error", Code: "store_unavailable", Message: "The response store was unavailable."}
		stored.fenced = true
		if stored.CancelFunc != nil {
			stored.CancelFunc()
		}
		return
	}
	s.armFlushTimer(d, id, stored)
}

// eventsAfterDurable reads events from the table when this process does not
// own the response.
func (s *ResponseStore) eventsAfterDurable(id string, startingAfter int) ([]StreamedEvent, bool, error) {
	d := s.durable()
	if d == nil {
		return nil, false, nil
	}
	events, err := d.eventsAfter(context.Background(), id, startingAfter)
	if err != nil && strings.HasPrefix(err.Error(), "response not found") {
		return nil, false, nil
	}
	return events, true, err
}

// statusOf returns the current status of a response, from the owner's memory
// or from the table.
func (s *ResponseStore) statusOf(id string) (string, error) {
	s.mu.RLock()
	stored, exists := s.responses[id]
	d := s.dur
	s.mu.RUnlock()
	if exists {
		stored.mu.RLock()
		defer stored.mu.RUnlock()
		return stored.Response.Status, nil
	}
	if d != nil {
		var row responseRow
		err := d.db.Select("status").Where("id = ?", id).Take(&row).Error
		if err == nil {
			return row.Status, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
	}
	if remote, ok := s.remoteGet(id); ok {
		return remote.Response.Status, nil
	}
	return "", fmt.Errorf("response not found: %s", id)
}

// SubscribeEvents returns a channel that signals new events of a response. The
// owner's live channel is used when this process owns it. Otherwise the table
// is polled until ctx ends.
func (s *ResponseStore) SubscribeEvents(ctx context.Context, id string) (<-chan struct{}, error) {
	s.mu.RLock()
	stored, exists := s.responses[id]
	d := s.dur
	s.mu.RUnlock()
	if exists {
		return stored.EventsChan, nil
	}
	if d == nil {
		if _, remote := s.remoteGet(id); remote {
			return nil, ErrResponseNotLocal
		}
		return nil, fmt.Errorf("response not found: %s", id)
	}
	ch := make(chan struct{}, 1)
	go func() {
		t := time.NewTicker(pollEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	}()
	return ch, nil
}

// leaseTick renews the leases of the running responses this process owns and
// applies the cancels and fences the table holds for them.
func (s *ResponseStore) leaseTick(ctx context.Context) {
	d := s.durable()
	if d == nil {
		return
	}
	s.mu.RLock()
	owned := make(map[string]*StoredResponse)
	for id, st := range s.responses {
		if !st.IsBackground {
			continue
		}
		st.mu.RLock()
		running := st.Response != nil && isNonTerminal(st.Response.Status)
		st.mu.RUnlock()
		if running {
			owned[id] = st
		}
	}
	s.mu.RUnlock()
	if len(owned) == 0 {
		return
	}
	ids := make([]string, 0, len(owned))
	for id := range owned {
		ids = append(ids, id)
	}
	if err := d.renewLeases(ctx, ids); err != nil {
		xlog.Warn("open responses: could not renew leases", "error", err)
		return
	}
	states, err := d.states(ctx, ids)
	if err != nil {
		xlog.Warn("open responses: could not read the state of running responses", "error", err)
		return
	}
	for _, st := range states {
		stored := owned[st.ID]
		switch {
		case st.CancelRequested && isNonTerminal(st.Status):
			if _, err := s.cancelLocal(st.ID, stored); err != nil {
				xlog.Warn("open responses: could not apply a cancel request", "response_id", st.ID, "error", err)
				continue
			}
			s.persist(st.ID, stored)
			s.mirror(st.ID, stored)
		case !isNonTerminal(st.Status):
			// The sweeper ended this run while we were away. Stop it and stop
			// writing, so the table keeps the one end state.
			stored.mu.Lock()
			stored.fenced = true
			if stored.CancelFunc != nil {
				stored.CancelFunc()
			}
			if stored.Response != nil && isNonTerminal(stored.Response.Status) {
				stored.Response.Status = st.Status
			}
			stored.mu.Unlock()
		}
	}
}

// SweepOnce ends the dead runs and deletes the expired rows. Any replica may
// run it; the writes are conditional.
func (s *ResponseStore) SweepOnce(ctx context.Context) {
	d := s.durable()
	if d == nil {
		return
	}
	if n, err := d.sweepDead(ctx, ""); err != nil {
		xlog.Warn("open responses: sweep failed", "error", err)
	} else if n > 0 {
		xlog.Info("Open Responses: ended responses whose server stopped", "responses", n)
	}
	if n, err := d.deleteExpired(ctx); err != nil {
		xlog.Warn("open responses: expiry sweep failed", "error", err)
	} else if n > 0 {
		xlog.Debug("Open Responses: deleted expired responses", "responses", n)
	}
}

// SweepOrphans ends every running response of another process. It is for a
// standalone start, where one process owns the database and so any row that is
// still running belongs to a process that is gone.
func (s *ResponseStore) SweepOrphans(ctx context.Context) (int, error) {
	d := s.durable()
	if d == nil {
		return 0, nil
	}
	return d.sweepDead(ctx, d.token)
}

// StartMaintenance runs the lease renewal and the sweeper until ctx ends. In a
// cluster one frontend at a time sweeps, the one that holds the advisory lock;
// standalone, this process sweeps once at start (for the runs a previous
// process left) and then on a timer.
func (s *ResponseStore) StartMaintenance(ctx context.Context, distributed bool) {
	d := s.durable()
	if d == nil {
		return
	}
	go func() {
		t := time.NewTicker(d.leaseEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.leaseTick(ctx)
			}
		}
	}()
	if distributed {
		go advisorylock.RunLeaderLoop(ctx, d.db, advisorylock.KeyOpenResponsesSweep, sweepEvery, func() { s.SweepOnce(ctx) })
		return
	}
	if n, err := s.SweepOrphans(ctx); err != nil {
		xlog.Warn("open responses: start sweep failed", "error", err)
	} else if n > 0 {
		xlog.Info("Open Responses: ended responses a previous process left running", "responses", n)
	}
	go func() {
		t := time.NewTicker(sweepEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.SweepOnce(ctx)
			}
		}
	}()
}

// flushAll writes every waiting event. Close calls it so a clean stop loses
// nothing.
func (s *ResponseStore) flushAll() {
	s.mu.RLock()
	snapshot := make(map[string]*StoredResponse, len(s.responses))
	for id, st := range s.responses {
		snapshot[id] = st
	}
	s.mu.RUnlock()
	for id, st := range snapshot {
		s.flushEvents(id, st)
	}
}
