// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func replayFixture() Workspace {
	g := DefaultGraph()
	g.Primary = "review"
	g.Nodes[0].Input.Value.Text = "tower"
	g.Nodes[1].Model = "mock"
	return New("tower", g, time.Unix(100, 0).UTC())
}

func roundTrip(w Workspace) Workspace {
	raw, err := json.Marshal(w)
	Expect(err).NotTo(HaveOccurred())
	Expect(string(raw)).NotTo(ContainSubstring(`"snapshot"`))

	var restored Workspace
	Expect(json.Unmarshal(raw, &restored)).To(Succeed())

	return restored
}

var _ = Describe("Replayable pipeline history", func() {
	It("lets parent inference finish after a fork without executing or completing the child", func() {
		ctx := context.Background()
		s, err := Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		adapter := &mockExecutor{entered: make(chan struct{}, 1), release: make(chan struct{})}
		r, err := NewRunner(ctx, s, adapter)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() { Expect(r.Close(ctx)).To(Succeed()) })
		g := replayFixture().Branches[0].Current().Graph
		g.Nodes[1].Pause = false
		w, err := r.Apply(ctx, "alice", "fork-live", "create", 0, Command{Action: "create", Graph: &g})
		Expect(err).NotTo(HaveOccurred())

		parent := w.Branches[0].ID
		_, err = r.Apply(ctx, "alice", "fork-live", "run", w.Revision, Command{Action: "run-node", Branch: parent, Node: "review"})
		Expect(err).NotTo(HaveOccurred())
		Eventually(adapter.entered).Should(Receive())
		Eventually(func() error {
			w, err = r.Read(ctx, "fork-live")
			if err != nil {
				return err
			}

			b, _ := w.Branch(parent)
			w, err = r.Apply(ctx, "alice", "fork-live", "fork", w.Revision, Command{Action: "fork", Branch: parent, Checkpoint: b.Events()[len(b.Events())-1].ID})
			return err
		}).Should(Succeed())
		child := w.Branches[len(w.Branches)-1].ID
		close(adapter.release)
		Eventually(func() Status {
			w, err := r.Read(ctx, "fork-live")
			if err != nil {
				return "error"
			}

			b, _ := w.Branch(parent)
			return b.Current().Nodes["review"].Status()
		}).Should(Equal(Succeeded))
		w, err = r.Read(ctx, "fork-live")
		Expect(err).NotTo(HaveOccurred())

		b, err := w.Branch(child)
		Expect(err).NotTo(HaveOccurred())
		Expect(b.Current().Nodes["review"].Status()).To(Equal(Stale))
		Expect(b.Current().Nodes["review"].Attempt.Status).To(Equal(Running))
		Expect(b.Current().Nodes["review"].Attempt.Outputs).To(BeEmpty())
		Expect(adapter.calls.Load()).To(Equal(int32(1)))
	})

	It("replays every checkpoint, records arguments once, and separates readiness from outcome", func() {
		w := replayFixture()
		b := w.Branches[0]
		now := time.Unix(110, 0).UTC()
		_, err := w.RunNode(b.ID, "review", now)
		Expect(err).NotTo(HaveOccurred())

		a := b.Current().Nodes["review"].Attempt
		started := b.Events()[len(b.Events())-1]
		for i := range 30 {
			Expect(w.Progress(b.ID, a.ID, "job", fmt.Sprintf("step %d", i), now)).To(Succeed())

			e := b.Events()[len(b.Events())-1]
			Expect(e.Type).To(Equal(schema.ExecutionEvent))
			Expect(e.Action).To(Equal(schema.Progress))

			raw, err := json.Marshal(e)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(raw)).NotTo(ContainSubstring(`"arguments"`))
			Expect(string(raw)).NotTo(ContainSubstring(`"graph"`))
		}
		Expect(w.Complete(b.ID, a.ID, []Value{{Type: "text", Text: "reviewed"}}, "advice", nil, nil, now)).To(Succeed())
		Expect(b.Current().Nodes["review"].Status()).To(Equal(Waiting))
		Expect(b.Current().Nodes["review"].Attempt.Status).To(Equal(Succeeded))
		Expect(w.Select(b.ID, "review", 0, nil, now)).To(Succeed())

		g := b.Current().Graph
		g.Nodes[0].Input.Value.Text = "changed"
		Expect(w.Configure(b.ID, g, now)).To(Succeed())
		Expect(b.Current().Nodes["review"].Status()).To(Equal(Stale))
		Expect(b.Current().Nodes["review"].Attempt.Status).To(Equal(Succeeded))

		restored := roundTrip(w)
		Expect(restored.Branches[0].Current()).To(Equal(b.Current()))

		for _, e := range b.Events() {
			before, err := b.Replay(e.ID)
			Expect(err).NotTo(HaveOccurred())

			after, err := restored.Branches[0].Replay(e.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
		}

		old, err := b.Replay(started.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(old.Nodes["review"].Attempt.Status).To(Equal(Running))
		Expect(old.Nodes["review"].Attempt.Progress).To(BeEmpty())

		old.Graph.Nodes[0].Input.Value.Text = "mutated caller copy"
		old.Nodes["review"].Attempt.Arguments.Input.Text = "mutated caller copy"
		again, err := b.Replay(started.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Nodes["review"].Attempt.Arguments.Input.Text).To(Equal("tower"))
	})

	It("shares only the ancestry prefix and detaches a running invocation without changing it", func() {
		w := replayFixture()
		parent := w.Branches[0]
		now := time.Now()
		_, err := w.RunNode(parent.ID, "review", now)
		Expect(err).NotTo(HaveOccurred())

		checkpoint := parent.Events()[len(parent.Events())-1]
		a := parent.Current().Nodes["review"].Attempt
		id, err := w.Fork(parent.ID, checkpoint.ID, now)
		Expect(err).NotTo(HaveOccurred())

		child, err := w.Branch(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(child.Events()).To(HaveLen(1))
		Expect(child.Parent).To(BeIdenticalTo(parent))
		Expect(child.Checkpoint()).To(Equal(checkpoint))
		Expect(child.History()[0]).To(Equal(parent.Events()[0]))
		Expect(child.Current().Busy()).To(BeFalse())
		Expect(child.Current().Nodes["review"].Status()).To(Equal(Stale))
		Expect(child.Current().Nodes["review"].Attempt.Status).To(Equal(Running))
		Expect(w.Complete(parent.ID, a.ID, []Value{{Type: "text", Text: "done"}}, "", nil, nil, now)).To(Succeed())
		Expect(child.Current().Nodes["review"].Attempt.Outputs).To(BeEmpty())

		_, err = child.Replay(parent.Events()[len(parent.Events())-1].ID)
		Expect(err).To(HaveOccurred())

		_, err = w.Fork(child.ID, parent.Events()[0].ID, now)
		Expect(err).NotTo(HaveOccurred())

		restored := roundTrip(w)
		Expect(restored.Branches[1].Parent).To(BeIdenticalTo(restored.Branches[0]))
		Expect(restored.Branches[1].Checkpoint()).To(Equal(restored.Branches[0].Events()[len(parent.Events())-2]))
		Expect(restored.Branches[1].Current()).To(Equal(child.Current()))

		view := MakeView(restored, child.ID, 0)
		visible := 0
		for _, e := range child.History() {
			if e.Message != "" {
				visible++
			}
		}
		Expect(view.Events).To(HaveLen(min(20, visible)))
	})

	It("rejects unsupported versions, dangling references and ancestry cycles", func() {
		w := replayFixture()
		raw, err := json.Marshal(w)
		Expect(err).NotTo(HaveOccurred())

		var doc map[string]any
		Expect(json.Unmarshal(raw, &doc)).To(Succeed())

		doc["version"] = 99
		invalid, err := json.Marshal(doc)
		Expect(err).NotTo(HaveOccurred())

		var decoded Workspace
		Expect(json.Unmarshal(invalid, &decoded)).NotTo(Succeed())

		doc["version"] = 2
		b := doc["branches"].([]any)[0].(map[string]any)
		b["parent"], b["checkpoint"] = b["id"], "missing"
		invalid, err = json.Marshal(doc)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(invalid, &decoded)).To(MatchError(ContainSubstring("cyclic")))

		b["parent"] = "missing"
		invalid, err = json.Marshal(doc)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(invalid, &decoded)).To(MatchError(ContainSubstring("parent branch not found")))

		delete(b, "parent")
		delete(b, "checkpoint")
		e := b["events"].([]any)[0].(map[string]any)
		e["version"] = 1
		invalid, err = json.Marshal(doc)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(invalid, &decoded)).To(MatchError(ContainSubstring("unsupported pipeline event version 1")))

		e["version"] = 99
		invalid, err = json.Marshal(doc)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(invalid, &decoded)).To(MatchError(ContainSubstring("unsupported pipeline event version")))

		e["version"] = 1
		e["changes"] = []any{map[string]any{"kind": "bind-node", "node": "review", "binding": map[string]any{"attempt": "missing"}}}
		invalid, err = json.Marshal(doc)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(invalid, &decoded)).To(MatchError(ContainSubstring(`unknown field "changes"`)))
	})

})

var _ = Describe("Append-only pipeline journal", func() {
	It("rolls back failed commands and preserves receipts across later appends", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		s, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		db, err := sql.Open("sqlite3", filepath.Join(dir, "pipelines.sqlite"))
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(db.Close)
		w := replayFixture()
		b := w.Branches[0]
		_, err = w.RunNode(b.ID, "review", time.Now())
		Expect(err).NotTo(HaveOccurred())

		a := b.Current().Nodes["review"].Attempt
		Expect(w.Complete(b.ID, a.ID, []Value{{Type: "text", Text: "done"}}, "", nil, nil, time.Now())).To(Succeed())

		_, err = w.Fork(b.ID, b.Events()[len(b.Events())-1].ID, time.Now())
		Expect(err).NotTo(HaveOccurred())

		w, err = s.Update(ctx, "resource", "create", 0, "create", func(saved *Workspace) error { *saved = w; return nil })
		Expect(err).NotTo(HaveOccurred())

		loaded, err := s.Read(ctx, "resource")
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded.Branches[0].Current()).To(Equal(b.Current()))
		Expect(loaded.Branches[1].Parent).To(BeIdenticalTo(loaded.Branches[0]))

		var count, beforeCount int
		Expect(db.QueryRow("SELECT COUNT(*) FROM pipeline_events").Scan(&beforeCount)).To(Succeed())

		var original []byte
		Expect(db.QueryRow("SELECT body FROM pipelines WHERE id='resource'").Scan(&original)).To(Succeed())

		_, err = s.Update(ctx, "resource", "failed", w.Revision, "failed", func(w *Workspace) error {
			g := b.Current().Graph
			g.Nodes[0].Input.Value.Text = "rolled back"
			if err := w.Configure(b.ID, g, time.Now()); err != nil {
				return err
			}

			return fmt.Errorf("deliberate command failure")
		})
		Expect(err).To(HaveOccurred())
		Expect(db.QueryRow("SELECT COUNT(*) FROM pipeline_events").Scan(&count)).To(Succeed())
		Expect(count).To(Equal(beforeCount))

		var afterFailure []byte
		Expect(db.QueryRow("SELECT body FROM pipelines WHERE id='resource'").Scan(&afterFailure)).To(Succeed())
		Expect(afterFailure).To(Equal(original))

		selectOutput := func(w *Workspace) error { return w.Select(b.ID, "review", 0, nil, time.Now()) }
		updated, err := s.Update(ctx, "resource", "select", w.Revision, "select", selectOutput)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.QueryRow("SELECT COUNT(*) FROM pipeline_events").Scan(&count)).To(Succeed())
		Expect(count).To(Equal(len(updated.Branches[0].Events()) + len(updated.Branches[1].Events())))

		_, err = s.Update(ctx, "resource", "retry", updated.Revision, "retry", func(w *Workspace) error {
			_, err := w.Retry(b.ID, "review", time.Now())
			return err
		})
		Expect(err).NotTo(HaveOccurred())

		receipt, err := s.Update(ctx, "resource", "select", w.Revision, "select", func(*Workspace) error {
			Fail("receipt must not repeat the command")
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.Branches).To(HaveLen(2))
		Expect(receipt.Branches[0].Current()).To(Equal(updated.Branches[0].Current()))

		var body string
		Expect(db.QueryRow("SELECT body FROM receipts WHERE operation='select'").Scan(&body)).To(Succeed())
		Expect(body).NotTo(ContainSubstring(`"arguments"`))
		Expect(body).NotTo(ContainSubstring(`"snapshot"`))
		Expect(s.Close()).To(Succeed())

		s, err = Open(dir)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		reloaded, err := s.Read(ctx, "resource")
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.Branches).To(HaveLen(3))
	})

	It("rejects edits and deletion of committed events without changing the head", func() {
		ctx := context.Background()
		s, err := Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		w, err := s.Update(ctx, "resource", "create", 0, "create", func(w *Workspace) error { *w = replayFixture(); return nil })
		Expect(err).NotTo(HaveOccurred())

		_, err = s.Update(ctx, "resource", "rewrite", w.Revision, "rewrite", func(w *Workspace) error {
			raw, err := json.Marshal(w)
			if err != nil {
				return err
			}
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				return err
			}
			branches := document["branches"].([]any)
			events := branches[0].(map[string]any)["events"].([]any)
			events[0].(map[string]any)["message"] = "rewritten"
			raw, err = json.Marshal(document)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, w); err != nil {
				return err
			}
			return nil
		})
		Expect(err).To(MatchError(ContainSubstring("cannot rewrite recorded event")))

		_, err = s.Update(ctx, "resource", "delete", w.Revision, "delete", func(w *Workspace) error {
			w.Branches = nil
			return nil
		})
		Expect(err).To(HaveOccurred())

		got, err := s.Read(ctx, "resource")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Revision).To(Equal(w.Revision))
		Expect(got.Branches[0].Events()[0].Message).NotTo(Equal("rewritten"))
	})
})
