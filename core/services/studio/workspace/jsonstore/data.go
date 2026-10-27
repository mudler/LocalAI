package jsonstore

import (
	"sort"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// rootID is the node ID of the root folder.
const rootID int64 = 1

// state is the part of a node that operations change and undo restores.
// Parent is 0 for the root and for a node that does not exist yet.
type state struct {
	Parent  int64  `json:"parent,omitempty"`
	Name    string `json:"name"`
	Blob    string `json:"blob,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

type nodeRow struct {
	ID      int64          `json:"id"`
	Kind    workspace.Kind `json:"kind"`
	Version int64          `json:"version"`
	Rev     int64          `json:"rev"`
	state
	fold string // cached workspace.FoldName of the name; not stored
}

// side is one end of a journal row: the state of a node before or after it.
type side struct {
	Parent  int64  `json:"parent,omitempty"`
	Name    string `json:"name,omitempty"`
	Path    string `json:"path,omitempty"`
	Blob    string `json:"blob,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

func (s side) state() state {
	return state{Parent: s.Parent, Name: s.Name, Blob: s.Blob, Size: s.Size, Deleted: s.Deleted}
}

// journalRow is one operation on one node. IDs count from 1 in the order of
// the journal.
type journalRow struct {
	ID   int64            `json:"id"`
	Rev  int64            `json:"rev"`
	Node int64            `json:"node"`
	Op   workspace.OpKind `json:"op"`
	Old  side             `json:"old"`
	New  side             `json:"new"`
}

// changeRec is a change set without its operations; those are the journal
// rows of its revision.
type changeRec struct {
	Rev       int64                   `json:"rev"`
	Actor     string                  `json:"actor"`
	ChatID    string                  `json:"chat_id,omitempty"`
	Title     string                  `json:"title"`
	Kind      workspace.ChangeSetKind `json:"kind"`
	Undoes    int64                   `json:"undoes,omitempty"`
	CreatedAt int64                   `json:"created_at"` // Unix milliseconds
}

type grantKey struct {
	Principal string
	Node      int64
}

// grantRec is a grant as stored. Mode 0 removes the grant.
type grantRec struct {
	Principal string         `json:"principal"`
	Node      int64          `json:"node"`
	Mode      workspace.Mode `json:"mode"`
}

type liveKey struct {
	parent int64
	fold   string
}

// undoEntry restores one node: prev is nil when the node did not exist.
type undoEntry struct {
	id   int64
	prev *nodeRow
}

// mark is a point in the open change set that rollbackTo returns to.
type mark struct{ undo, journal int }

// data is the whole index in memory: the tree, the journal, the change sets
// and the grants. The store guards it with one lock. Inside an open change set
// every change is logged, so it can be undone.
type data struct {
	nodes   map[int64]*nodeRow
	kids    map[int64]map[int64]struct{} // parent to the IDs of its children in any state
	live    map[liveKey]int64            // the live child of a folder by folded name
	grants  map[grantKey]workspace.Mode
	journal []journalRow
	byRev   map[int64][]int // journal positions by revision
	byNode  map[int64][]int // journal positions by node
	byNew   map[int64][]int // journal positions by the parent after the change
	sets    map[int64]*changeRec
	revs    []int64 // revisions of the change sets, ascending
	rev     int64
	nextID  int64
	seq     int64 // count of records written; replay skips what a snapshot has

	logging bool
	undo    []undoEntry
	dirty   map[int64]struct{}
}

func newData() *data {
	d := &data{
		nodes:  map[int64]*nodeRow{},
		kids:   map[int64]map[int64]struct{}{},
		live:   map[liveKey]int64{},
		grants: map[grantKey]workspace.Mode{},
		byRev:  map[int64][]int{},
		byNode: map[int64][]int{},
		byNew:  map[int64][]int{},
		sets:   map[int64]*changeRec{},
		nextID: rootID + 1,
	}
	root := nodeRow{ID: rootID, Kind: workspace.KindDir, Version: 1}
	d.nodes[rootID] = &root
	return d
}

func (d *data) index(n *nodeRow) {
	if n.Parent == 0 {
		return
	}
	if d.kids[n.Parent] == nil {
		d.kids[n.Parent] = map[int64]struct{}{}
	}
	d.kids[n.Parent][n.ID] = struct{}{}
	if !n.Deleted {
		d.live[liveKey{n.Parent, n.fold}] = n.ID
	}
}

func (d *data) unindex(n *nodeRow) {
	if n.Parent == 0 {
		return
	}
	delete(d.kids[n.Parent], n.ID)
	if k := (liveKey{n.Parent, n.fold}); d.live[k] == n.ID {
		delete(d.live, k)
	}
}

// setNode stores n. It refuses a live node whose folded name another live
// node of the same folder has.
func (d *data) setNode(n nodeRow) error {
	n.fold = workspace.FoldName(n.Name)
	if !n.Deleted && n.Parent != 0 {
		if other, ok := d.live[liveKey{n.Parent, n.fold}]; ok && other != n.ID {
			return workspace.ErrExists
		}
	}
	old := d.nodes[n.ID]
	if d.logging {
		e := undoEntry{id: n.ID}
		if old != nil {
			cp := *old
			e.prev = &cp
		}
		d.undo = append(d.undo, e)
		d.dirty[n.ID] = struct{}{}
	}
	if old != nil {
		d.unindex(old)
	}
	d.nodes[n.ID] = &n
	d.index(&n)
	if n.ID >= d.nextID {
		d.nextID = n.ID + 1
	}
	return nil
}

// restoreNode puts a node back as it was, or removes it.
func (d *data) restoreNode(e undoEntry) {
	if cur := d.nodes[e.id]; cur != nil {
		d.unindex(cur)
	}
	if e.prev == nil {
		delete(d.nodes, e.id)
		return
	}
	cp := *e.prev
	d.nodes[e.id] = &cp
	d.index(&cp)
}

func (d *data) appendJournal(r journalRow) {
	r.ID = int64(len(d.journal)) + 1
	i := len(d.journal)
	d.journal = append(d.journal, r)
	d.indexJournal(i)
}

func (d *data) indexJournal(i int) {
	r := d.journal[i]
	d.byRev[r.Rev] = append(d.byRev[r.Rev], i)
	d.byNode[r.Node] = append(d.byNode[r.Node], i)
	d.byNew[r.New.Parent] = append(d.byNew[r.New.Parent], i)
}

// truncateJournal drops the rows after the first n. They are the newest, so
// each is the last entry of its indexes.
func (d *data) truncateJournal(n int) {
	for len(d.journal) > n {
		i := len(d.journal) - 1
		r := d.journal[i]
		d.byRev[r.Rev] = d.byRev[r.Rev][:len(d.byRev[r.Rev])-1]
		d.byNode[r.Node] = d.byNode[r.Node][:len(d.byNode[r.Node])-1]
		d.byNew[r.New.Parent] = d.byNew[r.New.Parent][:len(d.byNew[r.New.Parent])-1]
		d.journal = d.journal[:i]
	}
}

func (d *data) beginTx() {
	d.logging = true
	d.undo = nil
	d.dirty = map[int64]struct{}{}
}

func (d *data) endTx() {
	d.logging = false
	d.undo = nil
	d.dirty = nil
}

func (d *data) mark() mark { return mark{undo: len(d.undo), journal: len(d.journal)} }

func (d *data) rollbackTo(m mark) {
	for i := len(d.undo) - 1; i >= m.undo; i-- {
		d.restoreNode(d.undo[i])
	}
	d.undo = d.undo[:m.undo]
	d.truncateJournal(m.journal)
}

// get returns the node with id in any state.
func (d *data) get(id int64) (nodeRow, error) {
	n, ok := d.nodes[id]
	if !ok {
		return nodeRow{}, workspace.ErrNotFound
	}
	return *n, nil
}

// childByName finds a live child by its exact name.
func (d *data) childByName(parent int64, name string) (nodeRow, bool) {
	id, ok := d.live[liveKey{parent, workspace.FoldName(name)}]
	if !ok {
		return nodeRow{}, false
	}
	n := d.nodes[id]
	if n.Name != name {
		return nodeRow{}, false
	}
	return *n, true
}

// childByFold finds a live child whose name folds to the same value as name.
func (d *data) childByFold(parent int64, name string) (nodeRow, bool) {
	id, ok := d.live[liveKey{parent, workspace.FoldName(name)}]
	if !ok {
		return nodeRow{}, false
	}
	return *d.nodes[id], true
}

// children returns the live children of a folder, sorted by name byte by
// byte.
func (d *data) children(id int64) []nodeRow {
	var out []nodeRow
	for kid := range d.kids[id] {
		if n := d.nodes[kid]; !n.Deleted {
			out = append(out, *n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lookup follows exact names from the root.
func (d *data) lookup(segs []string) (nodeRow, error) {
	n := *d.nodes[rootID]
	for _, s := range segs {
		if n.Kind != workspace.KindDir {
			return nodeRow{}, workspace.ErrNotFound
		}
		c, ok := d.childByName(n.ID, s)
		if !ok {
			return nodeRow{}, workspace.ErrNotFound
		}
		n = c
	}
	return n, nil
}

func (d *data) lookupPath(p string) (nodeRow, error) {
	segs, err := workspace.SplitPath(p)
	if err != nil {
		return nodeRow{}, err
	}
	return d.lookup(segs)
}

// deepestDir walks segs from the root. It returns the deepest live node on
// the path and how many segments lead to it. blocked is true when that node
// is a file that stands where a folder must be.
func (d *data) deepestDir(segs []string) (nodeRow, int, bool) {
	n := *d.nodes[rootID]
	for i, s := range segs {
		c, ok := d.childByName(n.ID, s)
		if !ok {
			return n, i, false
		}
		if c.Kind != workspace.KindDir {
			return c, i + 1, true
		}
		n = c
	}
	return n, len(segs), false
}

// pathOf returns the path of a node, live or not.
func (d *data) pathOf(id int64) string {
	var segs []string
	for n := d.nodes[id]; n != nil && n.Parent != 0; n = d.nodes[n.Parent] {
		segs = append(segs, n.Name)
	}
	for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
		segs[i], segs[j] = segs[j], segs[i]
	}
	return workspace.JoinPath(segs)
}

// ancestors returns the node and every folder above it, nearest first.
func (d *data) ancestors(id int64) []int64 {
	var out []int64
	for n := d.nodes[id]; n != nil; n = d.nodes[n.Parent] {
		out = append(out, n.ID)
		if n.Parent == 0 {
			break
		}
	}
	return out
}

// subtree returns the node and its live descendants: parents first, or
// deepest first when deepestFirst is true.
func (d *data) subtree(id int64, deepestFirst bool) []nodeRow {
	top, ok := d.nodes[id]
	if !ok {
		return nil
	}
	levels := [][]nodeRow{{*top}}
	for {
		var next []nodeRow
		for _, p := range levels[len(levels)-1] {
			next = append(next, d.liveKids(p.ID)...)
		}
		if len(next) == 0 {
			break
		}
		sort.Slice(next, func(i, j int) bool { return next[i].ID < next[j].ID })
		levels = append(levels, next)
	}
	var out []nodeRow
	if deepestFirst {
		for i := len(levels) - 1; i >= 0; i-- {
			out = append(out, levels[i]...)
		}
		return out
	}
	for _, l := range levels {
		out = append(out, l...)
	}
	return out
}

func (d *data) liveKids(id int64) []nodeRow {
	var out []nodeRow
	for kid := range d.kids[id] {
		if n := d.nodes[kid]; !n.Deleted {
			out = append(out, *n)
		}
	}
	return out
}
