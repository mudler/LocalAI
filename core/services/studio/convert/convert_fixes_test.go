package convert_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/convert"
)

func kbAgentPool(dir string) {
	write(filepath.Join(dir, "pool.json"), mustJSON(map[string]any{
		alice + ":Docs": map[string]any{"name": alice + ":Docs", "model": "m", "system_prompt": "x", "enable_kb": true},
	}))
}

func hasNotable(r convert.Report, name, status string) bool {
	for _, i := range r.Notable {
		if i.Name == name && i.Status == status {
			return true
		}
	}
	return false
}

func historyDB(n int) *gorm.DB {
	db := newDB()
	exec(db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
	for i := 0; i < n; i++ {
		conv := mustJSON(map[string]any{"name": fmt.Sprintf("Chat %03d", i), "model": "m", "createdAt": 1700000000000 + i,
			"history": []any{map[string]any{"role": "user", "content": "hi"}}})
		exec(db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, ?, ?, 1)`, alice, fmt.Sprintf("c%03d", i), conv)
	}
	return db
}

// unreadableMarks cannot read what an item became, as a database that is
// down would not.
type unreadableMarks struct{ convert.Marks }

func (unreadableMarks) Ref(context.Context, string) (string, bool, error) {
	return "", false, errors.New("marks store is down")
}

var _ = Describe("conversion fixes", func() {
	It("does not mark an agent that needs a collection grant while collections are unavailable", func() {
		dir := GinkgoT().TempDir()
		kbAgentPool(dir)
		tg := newTargets()
		tg.colErr = convert.ErrCollectionsUnavailable
		marks := memMarks()
		src := convert.Sources{StateDir: dir, AuthEnabled: true}
		r := run(src, tg, marks)
		it, _ := find(r, convert.KindAgent, "Docs")
		Expect(it.Status).To(Equal(convert.StatusFailed), "item = %+v", it)
		Expect(it.Reason).To(ContainSubstring("collections"), "item = %+v", it)
		Expect(tg.agents[alice]).To(BeEmpty(), "the agent was made without its grant")
		tg.colErr = nil
		tg.cols[alice] = []string{"Docs"}
		run(src, tg, marks)
		got := tg.agents[alice]
		Expect(got).To(HaveLen(1), "the retry did not convert with the grant: %+v", got)
		Expect(got[0].Collections).To(HaveLen(1), "the retry did not convert with the grant: %+v", got)
	})

	It("marks a dropped item so it is not new at the next run", func() {
		dir := GinkgoT().TempDir()
		write(filepath.Join(dir, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
			map[string]any{"id": "w", "name": "Hook", "model": "m", "prompt": "p", "webhooks": []any{map[string]any{}}},
		}}))
		tg := newTargets()
		marks := memMarks()
		src := convert.Sources{StateDir: dir, DataDir: dir}
		first := run(src, tg, marks)
		Expect(first.Notable).To(HaveLen(1), "first notable = %+v", first.Notable)
		Expect(first.Notable[0].Status).To(Equal(convert.StatusDropped))
		second := run(src, tg, marks)
		Expect(second.Notable).To(BeEmpty())
		it, _ := find(second, convert.KindTrigger, "Hook")
		Expect(it.Status).To(Equal(convert.StatusSkipped), "item = %+v", it)
		Expect(it.Reason).To(ContainSubstring("already handled"), "item = %+v", it)
	})

	It("drops an item the target refuses for good, with its reason, and does not try it again", func() {
		dir := GinkgoT().TempDir()
		poolFixture(dir)
		tg := newTargets()
		tg.invalidSave["Support"] = true
		marks := memMarks()
		src := convert.Sources{StateDir: dir, AuthEnabled: true}
		r := run(src, tg, marks)
		it, _ := find(r, convert.KindAgent, "Support")
		Expect(it.Status).To(Equal(convert.StatusDropped), "item = %+v", it)
		Expect(it.Reason).NotTo(BeEmpty(), "item = %+v", it)
		second := run(src, tg, marks)
		for _, n := range second.Notable {
			Expect(n.Name).NotTo(Equal("Support"), "the refused agent is news again: %+v", n)
		}
		Expect(tg.triggers[alice]).To(BeEmpty(), "a trigger was made for an agent that was dropped")
	})

	It("drops a trigger the target refuses for good instead of failing it", func() {
		dir := GinkgoT().TempDir()
		write(filepath.Join(dir, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
			map[string]any{"id": "c", "name": "Monthly", "model": "m", "prompt": "p", "cron": "0 0 1 JAN *"},
		}}))
		tg := newTargets()
		tg.trigErr = fmt.Errorf("%w: month names", convert.ErrInvalid)
		r := run(convert.Sources{StateDir: dir, DataDir: dir}, tg, memMarks())
		it, _ := find(r, convert.KindTrigger, "Monthly")
		Expect(it.Status).To(Equal(convert.StatusDropped), "item = %+v", it)
	})

	It("reports a failure as new only the first time it has that reason", func() {
		dir := GinkgoT().TempDir()
		poolFixture(dir)
		tg := newTargets()
		tg.failSave["Plain"] = true
		marks := memMarks()
		src := convert.Sources{StateDir: dir, AuthEnabled: true}
		first := run(src, tg, marks)
		Expect(hasNotable(first, "Plain", convert.StatusFailed)).To(BeTrue(), "first = %+v", first.Notable)
		second := run(src, tg, marks)
		Expect(hasNotable(second, "Plain", convert.StatusFailed)).To(BeFalse(), "the same failure is news again: %+v", second.Notable)
	})

	DescribeTable("the stored report keeps the first reason of an item",
		func(mk func() convert.Marks) {
			dir := GinkgoT().TempDir()
			poolFixture(dir)
			tg := newTargets()
			tg.agents[alice] = []convert.AgentIn{{Name: "Support"}}
			marks := mk()
			src := convert.Sources{StateDir: dir, AuthEnabled: true}
			run(src, tg, marks)
			run(src, tg, marks)
			stored, _, err := marks.Report(context.Background())
			Expect(err).NotTo(HaveOccurred())
			it, ok := find(stored, convert.KindAgent, "Support")
			Expect(ok).To(BeTrue(), "stored item = %+v", it)
			Expect(it.Status).To(Equal(convert.StatusConverted), "stored item = %+v", it)
			Expect(it.Reason).To(ContainSubstring("renamed to Support (2)"), "stored item = %+v", it)
			Expect(it.Reason).To(ContainSubstring("actions"), "stored item = %+v", it)
		},
		Entry("json", func() convert.Marks {
			m, err := convert.NewJSONMarks(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return m
		}),
		Entry("gorm", func() convert.Marks {
			m, err := convert.NewGORMMarks(newDB())
			Expect(err).NotTo(HaveOccurred())
			return m
		}),
	)

	It("does not convert a deleted agent row, nor its triggers or connectors", func() {
		db := newDB()
		exec(db, `CREATE TABLE agent_configs (id text, user_id text, name text, config text, status text)`)
		cfg := mustJSON(map[string]any{"name": "Gone", "model": "m", "system_prompt": "x", "standalone_job": true,
			"connectors": []any{map[string]any{"type": "telegram", "config": mustJSON(map[string]any{"token": token})}}})
		exec(db, `INSERT INTO agent_configs (id, user_id, name, config, status) VALUES ('1', ?, 'Gone', ?, 'deleted')`, alice, cfg)
		exec(db, `INSERT INTO agent_configs (id, user_id, name, config, status) VALUES ('2', ?, 'Null', ?, NULL)`, alice, mustJSON(map[string]any{"name": "Null", "model": "m"}))
		tg := newTargets()
		run(convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks())
		Expect(tg.agents[alice]).To(HaveLen(1), "agents %+v", tg.agents[alice])
		Expect(tg.agents[alice][0].Name).To(Equal("Null"))
		Expect(tg.triggers[alice]).To(BeEmpty())
		Expect(tg.bindings[alice]).To(BeEmpty())
	})

	It("makes a malformed source a failed item and still runs the other steps", func() {
		dir := GinkgoT().TempDir()
		write(filepath.Join(dir, "pool.json"), `{"truncated`)
		write(filepath.Join(dir, "users", alice, "agent_tasks.json"), `{"tasks": [`)
		write(filepath.Join(dir, "users", bob, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
			map[string]any{"id": "ok", "name": "Daily", "model": "m", "prompt": "p", "cron": "0 8 * * *"}}}))
		write(filepath.Join(dir, "skills", "s1", "SKILL.md"), "---\nname: s1\ndescription: d\n---\nbody")
		db := newDB()
		exec(db, `CREATE TABLE agent_configs (id text, user_id text, name text, config text, status text)`)
		exec(db, `INSERT INTO agent_configs (id, user_id, name, config, status) VALUES ('1', ?, 'Broken', '{not json', 'active')`, alice)
		tg := newTargets()
		r := run(convert.Sources{StateDir: dir, DB: db, AuthEnabled: true}, tg, memMarks())
		failed := 0
		for _, i := range r.Items {
			if i.Status == convert.StatusFailed {
				failed++
				Expect(i.Reason).NotTo(ContainSubstring(dir), "a local path is in the reason")
			}
		}
		Expect(failed).To(Equal(3), "failed = %d: %+v", failed, r.Items)
		Expect(tg.triggers[bob]).To(HaveLen(1), "bob's task was not converted: %+v", tg.triggers)
		Expect(tg.skills).To(HaveKey("shared/s1"), "the skill step did not run")
	})

	DescribeTable("the history is read in pages and every chat converts once",
		func(n int) {
			db := historyDB(n)
			tg := newTargets()
			marks := memMarks()
			src := convert.Sources{DB: db, AuthEnabled: true}
			run(src, tg, marks)
			run(src, tg, marks)
			Expect(tg.chats[alice]).To(HaveLen(n))
		},
		Entry("99 chats", 99),
		Entry("100 chats", 100),
		Entry("101 chats", 101),
		Entry("250 chats", 250),
	)

	It("discards a chat cut short by a crash and imports it once at the next run", func() {
		db := historyDB(1)
		tg := newTargets()
		tg.importErr = func(f *fakeTargets, u string, in convert.ChatIn) error {
			half := in
			half.Entries = nil
			f.chats[u] = append(f.chats[u], half) // what the process left before it died
			return errors.New("killed")
		}
		marks := memMarks()
		src := convert.Sources{DB: db, AuthEnabled: true}
		r := run(src, tg, marks)
		it, _ := find(r, convert.KindChat, "Chat 000")
		Expect(it.Status).To(Equal(convert.StatusFailed), "item = %+v", it)
		tg.importErr = nil
		r = run(src, tg, marks)
		Expect(tg.chats[alice]).To(HaveLen(1), "chats = %+v", tg.chats[alice])
		Expect(tg.chats[alice][0].Entries).To(HaveLen(1), "chats = %+v", tg.chats[alice])
		it, _ = find(r, convert.KindChat, "Chat 000")
		Expect(it.Status).To(Equal(convert.StatusConverted), "item = %+v", it)
	})

	It("keeps the zone the old tasks ran in on a converted schedule", func() {
		dir := GinkgoT().TempDir()
		write(filepath.Join(dir, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
			map[string]any{"id": "a", "name": "Morning", "model": "m", "prompt": "p", "cron": "0 8 * * *"}}}))
		tg := newTargets()
		r := run(convert.Sources{StateDir: dir, DataDir: dir, Zone: "Europe/Rome"}, tg, memMarks())
		got := tg.triggers[""]
		Expect(got).To(HaveLen(1), "triggers = %+v", got)
		Expect(got[0].TZ).To(Equal("Europe/Rome"))
		it, _ := find(r, convert.KindTrigger, "Morning")
		Expect(it.Reason).To(ContainSubstring("Europe/Rome"))
		// A zone that cannot be named says so in the report.
		write(filepath.Join(dir, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
			map[string]any{"id": "b", "name": "Evening", "model": "m", "prompt": "p", "cron": "0 20 * * *"}}}))
		r = run(convert.Sources{StateDir: dir, DataDir: dir, ZoneNote: "the server zone has no name"}, newTargets(), memMarks())
		it, _ = find(r, convert.KindTrigger, "Evening")
		Expect(it.Reason).To(ContainSubstring("the server zone has no name"))
	})

	It("cuts a tool result on a rune boundary", func() {
		db := newDB()
		exec(db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
		text := strings.Repeat("a", 499) + "€€€" // the euro sign straddles byte 500
		conv := mustJSON(map[string]any{"name": "T", "model": "m", "history": []any{map[string]any{"role": "tool_result", "name": "x", "content": text}}})
		exec(db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'c', ?, 1)`, alice, conv)
		tg := newTargets()
		run(convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks())
		got := tg.chats[alice][0].Entries[0].Text
		Expect(utf8.ValidString(got)).To(BeTrue(), "invalid UTF-8: %q", got)
	})

	// The old page kept a tool call and its result as messages whose content is a
	// JSON text: the name and the result are inside it, not beside it.
	It("reads a tool use of the old page from the JSON of its messages", func() {
		db := newDB()
		exec(db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
		call := mustJSON(map[string]any{"type": "tool_call", "name": "search", "arguments": map[string]any{"q": "x"}})
		result := mustJSON(map[string]any{"type": "tool_result", "name": "search", "result": "found it"})
		history := []any{
			map[string]any{"role": "user", "content": "look"},
			map[string]any{"role": "tool_call", "content": string(call), "expanded": false},
			map[string]any{"role": "tool_result", "content": string(result), "expanded": false},
			map[string]any{"role": "assistant", "content": "done"},
		}
		conv := mustJSON(map[string]any{"name": "T", "model": "m", "history": history})
		exec(db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'c', ?, 1)`, alice, conv)
		tg := newTargets()
		run(convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks())
		entries := tg.chats[alice][0].Entries
		Expect(entries).To(HaveLen(3), "entries = %+v", entries)
		Expect(entries[1].Role).To(Equal("tool"), "tool line = %+v", entries[1])
		Expect(entries[1].Text).To(Equal("result of search: found it"), "tool line = %+v", entries[1])
	})

	// Pictures and files of the old page are not carried over; the report says so.
	It("counts the attachments of the old page in the report", func() {
		db := newDB()
		exec(db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
		history := []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "what is this?"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
			}, "files": []any{map[string]any{"name": "a.pdf", "type": "file"}, map[string]any{"name": "b.wav", "type": "audio"}}},
			map[string]any{"role": "assistant", "content": "a cat"},
		}
		conv := mustJSON(map[string]any{"name": "Pics", "model": "m", "createdAt": 1700000000000, "history": history})
		exec(db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'c', ?, 1)`, alice, conv)
		tg := newTargets()
		r := run(convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks())
		it, ok := find(r, convert.KindChat, "Pics")
		Expect(ok).To(BeTrue(), "item = %+v", it)
		Expect(it.Status).To(Equal(convert.StatusConverted), "item = %+v", it)
		Expect(it.Reason).To(ContainSubstring("3 attachments"), "item = %+v", it)
		for _, e := range tg.chats[alice][0].Entries {
			Expect(e.Text).NotTo(ContainSubstring("base64"), "image data in the transcript")
		}
	})

	It("does not convert an item again when its mark cannot be read", func() {
		dir := GinkgoT().TempDir()
		write(filepath.Join(dir, "pool.json"), mustJSON(map[string]any{
			alice + ":Docs": map[string]any{"name": alice + ":Docs", "model": "m", "system_prompt": "x"},
		}))
		tg := newTargets()
		marks := memMarks()
		src := convert.Sources{StateDir: dir, AuthEnabled: true}

		run(src, tg, marks) // the agent is made once
		Expect(tg.agents[alice]).To(HaveLen(1), "first run made %d agents", len(tg.agents[alice]))
		r := run(src, tg, unreadableMarks{marks})
		Expect(tg.agents[alice]).To(HaveLen(1), "an unreadable mark made a second agent: %+v", tg.agents[alice])
		it, _ := find(r, convert.KindAgent, "Docs")
		Expect(it.Status).To(Equal(convert.StatusFailed), "item = %+v", it)
		Expect(it.Reason).To(ContainSubstring("marks store is down"), "item = %+v", it)
	})
})
