package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// writer is one change set in progress: one SQL transaction at revision rev.
type writer struct {
	s      *Workspace
	ctx    context.Context
	tx     *tx
	actor  Actor
	rev    int64
	undoes int64
	ops    int
	done   bool

	afterCommit func() // test hook, runs after the SQL commit
}

func (s *Workspace) beginWrite(ctx context.Context, a Actor, title string, kind ChangeSetKind) (*writer, error) {
	tx, err := s.db.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	// Taking the revision row first makes writers wait for each other on both
	// databases: SQLite takes its write lock and PostgreSQL the row lock, so
	// the revision read below is the one this change set owns.
	if _, err := tx.ExecContext(ctx, `UPDATE studio_ws_meta SET value = value WHERE key = 'rev'`); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	var cur int64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(value AS BIGINT) FROM studio_ws_meta WHERE key = 'rev'`).Scan(&cur); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	w := &writer{s: s, ctx: ctx, tx: tx, actor: a, rev: cur + 1}
	if _, err := tx.ExecContext(ctx, `INSERT INTO studio_ws_changesets (rev, actor, chat_id, title, kind, undoes, created_at) VALUES (?, ?, ?, ?, ?, 0, ?)`,
		w.rev, a.Principal, a.ChatID, title, string(kind), s.now().UnixMilli()); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return w, nil
}

func (w *writer) rollback() {
	if !w.done {
		w.done = true
		_ = w.tx.Rollback()
	}
}

// commit writes the change set. A writer without operations commits nothing
// and returns a zero ChangeSet.
func (w *writer) commit() (ChangeSet, error) {
	if w.done {
		return ChangeSet{}, ErrInvalid
	}
	if w.ops == 0 {
		w.rollback()
		return ChangeSet{}, nil
	}
	if _, err := w.tx.ExecContext(w.ctx, `UPDATE studio_ws_changesets SET undoes = ? WHERE rev = ?`, w.undoes, w.rev); err != nil {
		w.rollback()
		return ChangeSet{}, err
	}
	if _, err := w.tx.ExecContext(w.ctx, `UPDATE studio_ws_meta SET value = ? WHERE key = 'rev'`, strconv.FormatInt(w.rev, 10)); err != nil {
		w.rollback()
		return ChangeSet{}, err
	}
	w.done = true
	if err := w.tx.Commit(); err != nil {
		return ChangeSet{}, err
	}
	if w.afterCommit != nil {
		w.afterCommit()
	}
	cs, err := w.s.loadChangeSet(w.ctx, w.s.db, w.rev)
	if err != nil {
		// The change set is durable. Say so, so that the caller does not retry.
		return ChangeSet{Rev: w.rev, Actor: w.actor.Principal, ChatID: w.actor.ChatID, Undoes: w.undoes},
			fmt.Errorf("%w: loading the change set: %w", ErrCommitted, err)
	}
	return cs, nil
}

// step runs fn inside a savepoint. When fn fails, its changes are undone and
// the writer stays usable.
func (w *writer) step(fn func() error) error {
	if w.done {
		return ErrInvalid
	}
	if _, err := w.tx.ExecContext(w.ctx, `SAVEPOINT step`); err != nil {
		return err
	}
	ops := w.ops
	if err := fn(); err != nil {
		w.ops = ops
		_, rbErr := w.tx.ExecContext(w.ctx, `ROLLBACK TO step`)
		if rbErr == nil {
			_, rbErr = w.tx.ExecContext(w.ctx, `RELEASE step`)
		}
		if rbErr != nil {
			w.rollback()
			return errors.Join(err, rbErr)
		}
		return err
	}
	if _, err := w.tx.ExecContext(w.ctx, `RELEASE step`); err != nil {
		w.rollback()
		return err
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// apply moves node id (0 for a new node) to the state ns and journals the
// operation with the full state before and after.
func (w *writer) apply(id int64, kind Kind, op OpKind, ns state) (nodeRow, error) {
	ctx, q := w.ctx, w.tx
	before := state{deleted: true}
	beforePath := ""
	if id != 0 {
		cur, err := getNode(ctx, q, id)
		if err != nil {
			return nodeRow{}, err
		}
		before = cur.state
		if beforePath, err = pathOf(ctx, q, id); err != nil {
			return nodeRow{}, err
		}
	}
	if id == 0 {
		if err := q.QueryRowContext(ctx, `INSERT INTO studio_ws_nodes (parent_id, name, name_fold, kind, blob, size, version, rev, deleted) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?) RETURNING id`,
			nullID(ns.parent), ns.name, foldName(ns.name), string(kind), ns.blob, ns.size, w.rev, b2i(ns.deleted)).Scan(&id); err != nil {
			return nodeRow{}, mapUnique(err)
		}
	} else if _, err := q.ExecContext(ctx, `UPDATE studio_ws_nodes SET parent_id = ?, name = ?, name_fold = ?, blob = ?, size = ?, deleted = ?, version = version + 1, rev = ? WHERE id = ?`,
		nullID(ns.parent), ns.name, foldName(ns.name), ns.blob, ns.size, b2i(ns.deleted), w.rev, id); err != nil {
		return nodeRow{}, mapUnique(err)
	}
	afterPath, err := pathOf(ctx, q, id)
	if err != nil {
		return nodeRow{}, err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO studio_ws_journal (rev, node_id, op,
			old_parent, old_name, old_path, old_blob, old_size, old_deleted,
			new_parent, new_name, new_path, new_blob, new_size, new_deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.rev, id, string(op),
		nullID(before.parent), before.name, beforePath, before.blob, before.size, b2i(before.deleted),
		nullID(ns.parent), ns.name, afterPath, ns.blob, ns.size, b2i(ns.deleted)); err != nil {
		return nodeRow{}, err
	}
	w.ops++
	return getNode(ctx, q, id)
}

func (w *writer) checkWrite(n nodeRow) error {
	return w.s.needWrite(w.ctx, w.tx, w.actor, n)
}

// ensureDirs creates the missing folders of segs and returns the last one.
// The caller checks access first.
func (w *writer) ensureDirs(segs []string) (nodeRow, error) {
	n, covered, blocked, err := deepestDir(w.ctx, w.tx, segs)
	if err != nil {
		return nodeRow{}, err
	}
	if blocked {
		return nodeRow{}, ErrExists
	}
	for _, s := range segs[covered:] {
		if _, taken, err := childByFold(w.ctx, w.tx, n.id, s); err != nil {
			return nodeRow{}, err
		} else if taken {
			return nodeRow{}, ErrExists
		}
		if n, err = w.apply(0, KindDir, OpCreate, state{parent: n.id, name: s}); err != nil {
			return nodeRow{}, err
		}
	}
	return n, nil
}

// parentForWrite checks write access on the deepest existing folder of dir
// and creates the missing folders.
func (w *writer) parentForWrite(dir []string) (nodeRow, error) {
	anc, _, blocked, err := deepestDir(w.ctx, w.tx, dir)
	if err != nil {
		return nodeRow{}, err
	}
	if err := w.checkWrite(anc); err != nil {
		return nodeRow{}, err
	}
	if blocked {
		return nodeRow{}, ErrExists
	}
	return w.ensureDirs(dir)
}

// put writes a blob at segs. It returns the node and, for a conflict copy,
// the path the caller asked for.
func (w *writer) put(segs []string, sum string, size int64, o PutOptions) (nodeRow, string, error) {
	if len(segs) == 0 {
		return nodeRow{}, "", ErrInvalid
	}
	dir, name := segs[:len(segs)-1], segs[len(segs)-1]
	anc, covered, blocked, err := deepestDir(w.ctx, w.tx, dir)
	if err != nil {
		return nodeRow{}, "", err
	}
	if !blocked && covered == len(dir) {
		if ex, ok, err := childByName(w.ctx, w.tx, anc.id, name); err != nil {
			return nodeRow{}, "", err
		} else if ok {
			return w.overwrite(ex, sum, size, o)
		}
	}
	parent, err := w.parentForWrite(dir)
	if err != nil {
		return nodeRow{}, "", err
	}
	if _, taken, err := childByFold(w.ctx, w.tx, parent.id, name); err != nil {
		return nodeRow{}, "", err
	} else if taken {
		return nodeRow{}, "", ErrExists
	}
	n, err := w.apply(0, KindFile, OpCreate, state{parent: parent.id, name: name, blob: sum, size: size})
	return n, "", err
}

func (w *writer) overwrite(ex nodeRow, sum string, size int64, o PutOptions) (nodeRow, string, error) {
	if err := w.checkWrite(ex); err != nil {
		return nodeRow{}, "", err
	}
	if ex.kind != KindFile {
		return nodeRow{}, "", ErrExists
	}
	if ex.blob == sum {
		return ex, "", nil
	}
	if o.Overwrite || o.BaseVersion == ex.version {
		ns := ex.state
		ns.blob, ns.size = sum, size
		n, err := w.apply(ex.id, KindFile, OpWrite, ns)
		return n, "", err
	}
	n, err := w.conflictCopy(ex.parent, ex.name, sum, size)
	if err != nil {
		return nodeRow{}, "", err
	}
	asked, err := pathOf(w.ctx, w.tx, ex.id)
	return n, asked, err
}

// conflictCopy stores a blob next to name under a free conflict name.
func (w *writer) conflictCopy(parent int64, name, sum string, size int64) (nodeRow, error) {
	for i := 1; i < 1000; i++ {
		cand, err := cleanName(conflictName(name, label(w.actor), i))
		if err != nil {
			return nodeRow{}, err
		}
		if _, taken, err := childByFold(w.ctx, w.tx, parent, cand); err != nil {
			return nodeRow{}, err
		} else if !taken {
			return w.apply(0, KindFile, OpCreate, state{parent: parent, name: cand, blob: sum, size: size})
		}
	}
	return nodeRow{}, ErrExists
}

func (w *writer) mkdir(segs []string) (nodeRow, error) {
	if len(segs) == 0 {
		root, err := getNode(w.ctx, w.tx, rootID)
		if err != nil {
			return nodeRow{}, err
		}
		if err := w.checkWrite(root); err != nil {
			return nodeRow{}, err
		}
		return nodeRow{}, ErrExists
	}
	anc, covered, blocked, err := deepestDir(w.ctx, w.tx, segs)
	if err != nil {
		return nodeRow{}, err
	}
	if blocked || covered == len(segs) {
		if err := w.checkWrite(anc); err != nil {
			return nodeRow{}, err
		}
		return nodeRow{}, ErrExists
	}
	return w.parentForWrite(segs)
}

// loadChangeSet reads one change set with its operations.
func (s *Workspace) loadChangeSet(ctx context.Context, q querier, rev int64) (ChangeSet, error) {
	var cs ChangeSet
	var kind string
	var ms int64
	err := q.QueryRowContext(ctx, `SELECT rev, actor, chat_id, title, kind, undoes, created_at FROM studio_ws_changesets WHERE rev = ?`, rev).
		Scan(&cs.Rev, &cs.Actor, &cs.ChatID, &cs.Title, &kind, &cs.Undoes, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangeSet{}, ErrNotFound
	}
	if err != nil {
		return ChangeSet{}, err
	}
	cs.Kind = ChangeSetKind(kind)
	cs.CreatedAt = time.UnixMilli(ms).UTC()
	rows, err := q.QueryContext(ctx, `SELECT node_id, op, old_path, new_path, old_blob, new_blob FROM studio_ws_journal WHERE rev = ? ORDER BY id`, rev)
	if err != nil {
		return ChangeSet{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var o Op
		var k string
		if err := rows.Scan(&o.NodeID, &k, &o.OldPath, &o.NewPath, &o.OldBlob, &o.NewBlob); err != nil {
			return ChangeSet{}, err
		}
		o.Kind = OpKind(k)
		cs.Ops = append(cs.Ops, o)
	}
	return cs, rows.Err()
}
