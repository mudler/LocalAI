package convert_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/convert"
)

func kbAgentPool(t *testing.T, dir string) {
	t.Helper()
	write(t, filepath.Join(dir, "pool.json"), mustJSON(map[string]any{
		alice + ":Docs": map[string]any{"name": alice + ":Docs", "model": "m", "system_prompt": "x", "enable_kb": true},
	}))
}

func TestAnAgentThatNeedsACollectionGrantIsNotMarkedWhileCollectionsAreUnavailable(t *testing.T) {
	dir := t.TempDir()
	kbAgentPool(t, dir)
	tg := newTargets()
	tg.colErr = convert.ErrCollectionsUnavailable
	marks := memMarks(t)
	src := convert.Sources{StateDir: dir, AuthEnabled: true}
	r := run(t, src, tg, marks)
	if it, _ := find(r, convert.KindAgent, "Docs"); it.Status != convert.StatusFailed || !strings.Contains(it.Reason, "collections") {
		t.Fatalf("item = %+v", it)
	}
	if len(tg.agents[alice]) != 0 {
		t.Fatalf("the agent was made without its grant: %+v", tg.agents[alice])
	}
	tg.colErr = nil
	tg.cols[alice] = []string{"Docs"}
	run(t, src, tg, marks)
	if got := tg.agents[alice]; len(got) != 1 || len(got[0].Collections) != 1 {
		t.Fatalf("the retry did not convert with the grant: %+v", got)
	}
}

func TestADroppedItemIsMarkedAndNotNewAtTheNextRun(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
		map[string]any{"id": "w", "name": "Hook", "model": "m", "prompt": "p", "webhooks": []any{map[string]any{}}},
	}}))
	tg := newTargets()
	marks := memMarks(t)
	src := convert.Sources{StateDir: dir, DataDir: dir}
	first := run(t, src, tg, marks)
	if len(first.Notable) != 1 || first.Notable[0].Status != convert.StatusDropped {
		t.Fatalf("first notable = %+v", first.Notable)
	}
	second := run(t, src, tg, marks)
	if len(second.Notable) != 0 {
		t.Fatalf("second notable = %+v", second.Notable)
	}
	if it, _ := find(second, convert.KindTrigger, "Hook"); it.Status != convert.StatusSkipped || !strings.Contains(it.Reason, "already handled") {
		t.Fatalf("item = %+v", it)
	}
}

func TestAnItemTheTargetRefusesForGoodIsDroppedWithItsReasonAndNotTriedAgain(t *testing.T) {
	dir := t.TempDir()
	poolFixture(t, dir)
	tg := newTargets()
	tg.invalidSave["Support"] = true
	marks := memMarks(t)
	src := convert.Sources{StateDir: dir, AuthEnabled: true}
	r := run(t, src, tg, marks)
	it, _ := find(r, convert.KindAgent, "Support")
	if it.Status != convert.StatusDropped || it.Reason == "" {
		t.Fatalf("item = %+v", it)
	}
	second := run(t, src, tg, marks)
	for _, n := range second.Notable {
		if n.Name == "Support" {
			t.Fatalf("the refused agent is news again: %+v", n)
		}
	}
	for _, i := range tg.triggers[alice] {
		t.Fatalf("a trigger was made for an agent that was dropped: %+v", i)
	}
}

func TestATriggerTheTargetRefusesForGoodIsDroppedNotFailed(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
		map[string]any{"id": "c", "name": "Monthly", "model": "m", "prompt": "p", "cron": "0 0 1 JAN *"},
	}}))
	tg := newTargets()
	tg.trigErr = fmt.Errorf("%w: month names", convert.ErrInvalid)
	r := run(t, convert.Sources{StateDir: dir, DataDir: dir}, tg, memMarks(t))
	if it, _ := find(r, convert.KindTrigger, "Monthly"); it.Status != convert.StatusDropped {
		t.Fatalf("item = %+v", it)
	}
}

func TestAFailureIsNewOnlyTheFirstTimeItHasThatReason(t *testing.T) {
	dir := t.TempDir()
	poolFixture(t, dir)
	tg := newTargets()
	tg.failSave["Plain"] = true
	marks := memMarks(t)
	src := convert.Sources{StateDir: dir, AuthEnabled: true}
	first := run(t, src, tg, marks)
	if !hasNotable(first, "Plain", convert.StatusFailed) {
		t.Fatalf("first = %+v", first.Notable)
	}
	if second := run(t, src, tg, marks); hasNotable(second, "Plain", convert.StatusFailed) {
		t.Fatalf("the same failure is news again: %+v", second.Notable)
	}
}

func hasNotable(r convert.Report, name, status string) bool {
	for _, i := range r.Notable {
		if i.Name == name && i.Status == status {
			return true
		}
	}
	return false
}

func marksKinds(t *testing.T) map[string]func(t *testing.T) convert.Marks {
	return map[string]func(t *testing.T) convert.Marks{
		"json": func(t *testing.T) convert.Marks {
			m, err := convert.NewJSONMarks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return m
		},
		"gorm": func(t *testing.T) convert.Marks {
			m, err := convert.NewGORMMarks(newDB(t))
			if err != nil {
				t.Fatal(err)
			}
			return m
		},
	}
}

func TestTheStoredReportKeepsTheFirstReasonOfAnItem(t *testing.T) {
	for kind, mk := range marksKinds(t) {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			poolFixture(t, dir)
			tg := newTargets()
			tg.agents[alice] = []convert.AgentIn{{Name: "Support"}}
			marks := mk(t)
			src := convert.Sources{StateDir: dir, AuthEnabled: true}
			run(t, src, tg, marks)
			run(t, src, tg, marks)
			stored, _, err := marks.Report(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			it, ok := find(stored, convert.KindAgent, "Support")
			if !ok || it.Status != convert.StatusConverted || !strings.Contains(it.Reason, "renamed to Support (2)") || !strings.Contains(it.Reason, "actions") {
				t.Fatalf("stored item = %+v", it)
			}
		})
	}
}

func TestADeletedAgentRowIsNotConvertedAndNeitherAreItsTriggersOrConnectors(t *testing.T) {
	db := newDB(t)
	exec(t, db, `CREATE TABLE agent_configs (id text, user_id text, name text, config text, status text)`)
	cfg := mustJSON(map[string]any{"name": "Gone", "model": "m", "system_prompt": "x", "standalone_job": true,
		"connectors": []any{map[string]any{"type": "telegram", "config": mustJSON(map[string]any{"token": token})}}})
	exec(t, db, `INSERT INTO agent_configs (id, user_id, name, config, status) VALUES ('1', ?, 'Gone', ?, 'deleted')`, alice, cfg)
	exec(t, db, `INSERT INTO agent_configs (id, user_id, name, config, status) VALUES ('2', ?, 'Null', ?, NULL)`, alice, mustJSON(map[string]any{"name": "Null", "model": "m"}))
	tg := newTargets()
	run(t, convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks(t))
	if len(tg.agents[alice]) != 1 || tg.agents[alice][0].Name != "Null" || len(tg.triggers[alice]) != 0 || len(tg.bindings[alice]) != 0 {
		t.Fatalf("agents %+v triggers %+v bindings %+v", tg.agents[alice], tg.triggers[alice], tg.bindings[alice])
	}
}

func TestAMalformedSourceIsAFailedItemAndTheOtherStepsStillRun(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pool.json"), `{"truncated`)
	write(t, filepath.Join(dir, "users", alice, "agent_tasks.json"), `{"tasks": [`)
	write(t, filepath.Join(dir, "users", bob, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
		map[string]any{"id": "ok", "name": "Daily", "model": "m", "prompt": "p", "cron": "0 8 * * *"}}}))
	write(t, filepath.Join(dir, "skills", "s1", "SKILL.md"), "---\nname: s1\ndescription: d\n---\nbody")
	db := newDB(t)
	exec(t, db, `CREATE TABLE agent_configs (id text, user_id text, name text, config text, status text)`)
	exec(t, db, `INSERT INTO agent_configs (id, user_id, name, config, status) VALUES ('1', ?, 'Broken', '{not json', 'active')`, alice)
	tg := newTargets()
	r := run(t, convert.Sources{StateDir: dir, DB: db, AuthEnabled: true}, tg, memMarks(t))
	failed := 0
	for _, i := range r.Items {
		if i.Status == convert.StatusFailed {
			failed++
			if strings.Contains(i.Reason, dir) {
				t.Fatalf("a local path is in the reason: %q", i.Reason)
			}
		}
	}
	if failed != 3 {
		t.Fatalf("failed = %d: %+v", failed, r.Items)
	}
	if len(tg.triggers[bob]) != 1 {
		t.Fatalf("bob's task was not converted: %+v", tg.triggers)
	}
	if _, ok := tg.skills["shared/s1"]; !ok {
		t.Fatal("the skill step did not run")
	}
}

func historyDB(t *testing.T, n int) *gorm.DB {
	t.Helper()
	db := newDB(t)
	exec(t, db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
	for i := 0; i < n; i++ {
		conv := mustJSON(map[string]any{"name": fmt.Sprintf("Chat %03d", i), "model": "m", "createdAt": 1700000000000 + i,
			"history": []any{map[string]any{"role": "user", "content": "hi"}}})
		exec(t, db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, ?, ?, 1)`, alice, fmt.Sprintf("c%03d", i), conv)
	}
	return db
}

func TestTheHistoryIsReadInPagesAndEveryChatConvertsOnce(t *testing.T) {
	for _, n := range []int{99, 100, 101, 250} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db := historyDB(t, n)
			tg := newTargets()
			marks := memMarks(t)
			src := convert.Sources{DB: db, AuthEnabled: true}
			run(t, src, tg, marks)
			run(t, src, tg, marks)
			if len(tg.chats[alice]) != n {
				t.Fatalf("chats = %d, want %d", len(tg.chats[alice]), n)
			}
		})
	}
}

func TestAChatCutShortByACrashIsDiscardedAndImportedOnceAtTheNextRun(t *testing.T) {
	db := historyDB(t, 1)
	tg := newTargets()
	tg.importErr = func(f *fakeTargets, u string, in convert.ChatIn) error {
		half := in
		half.Entries = nil
		f.chats[u] = append(f.chats[u], half) // what the process left before it died
		return errors.New("killed")
	}
	marks := memMarks(t)
	src := convert.Sources{DB: db, AuthEnabled: true}
	r := run(t, src, tg, marks)
	if it, _ := find(r, convert.KindChat, "Chat 000"); it.Status != convert.StatusFailed {
		t.Fatalf("item = %+v", it)
	}
	tg.importErr = nil
	r = run(t, src, tg, marks)
	if len(tg.chats[alice]) != 1 || len(tg.chats[alice][0].Entries) != 1 {
		t.Fatalf("chats = %+v", tg.chats[alice])
	}
	if it, _ := find(r, convert.KindChat, "Chat 000"); it.Status != convert.StatusConverted {
		t.Fatalf("item = %+v", it)
	}
}

func TestAConvertedScheduleKeepsTheZoneTheOldTasksRanIn(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
		map[string]any{"id": "a", "name": "Morning", "model": "m", "prompt": "p", "cron": "0 8 * * *"}}}))
	tg := newTargets()
	r := run(t, convert.Sources{StateDir: dir, DataDir: dir, Zone: "Europe/Rome"}, tg, memMarks(t))
	if got := tg.triggers[""]; len(got) != 1 || got[0].TZ != "Europe/Rome" {
		t.Fatalf("triggers = %+v", got)
	}
	if it, _ := find(r, convert.KindTrigger, "Morning"); !strings.Contains(it.Reason, "Europe/Rome") {
		t.Fatalf("reason = %q", it.Reason)
	}
	// A zone that cannot be named says so in the report.
	write(t, filepath.Join(dir, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
		map[string]any{"id": "b", "name": "Evening", "model": "m", "prompt": "p", "cron": "0 20 * * *"}}}))
	r = run(t, convert.Sources{StateDir: dir, DataDir: dir, ZoneNote: "the server zone has no name"}, newTargets(), memMarks(t))
	if it, _ := find(r, convert.KindTrigger, "Evening"); !strings.Contains(it.Reason, "the server zone has no name") {
		t.Fatalf("reason = %q", it.Reason)
	}
}

func TestAToolResultIsCutOnARuneBoundary(t *testing.T) {
	db := newDB(t)
	exec(t, db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
	text := strings.Repeat("a", 499) + "€€€" // the euro sign straddles byte 500
	conv := mustJSON(map[string]any{"name": "T", "model": "m", "history": []any{map[string]any{"role": "tool_result", "name": "x", "content": text}}})
	exec(t, db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'c', ?, 1)`, alice, conv)
	tg := newTargets()
	run(t, convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks(t))
	got := tg.chats[alice][0].Entries[0].Text
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
}

// The old page kept a tool call and its result as messages whose content is a
// JSON text: the name and the result are inside it, not beside it.
func TestAToolUseOfTheOldPageIsReadFromTheJSONOfItsMessages(t *testing.T) {
	db := newDB(t)
	exec(t, db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
	call := mustJSON(map[string]any{"type": "tool_call", "name": "search", "arguments": map[string]any{"q": "x"}})
	result := mustJSON(map[string]any{"type": "tool_result", "name": "search", "result": "found it"})
	history := []any{
		map[string]any{"role": "user", "content": "look"},
		map[string]any{"role": "tool_call", "content": string(call), "expanded": false},
		map[string]any{"role": "tool_result", "content": string(result), "expanded": false},
		map[string]any{"role": "assistant", "content": "done"},
	}
	conv := mustJSON(map[string]any{"name": "T", "model": "m", "history": history})
	exec(t, db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'c', ?, 1)`, alice, conv)
	tg := newTargets()
	run(t, convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks(t))
	entries := tg.chats[alice][0].Entries
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	if got := entries[1]; got.Role != "tool" || got.Text != "result of search: found it" {
		t.Fatalf("tool line = %+v", got)
	}
}

// Pictures and files of the old page are not carried over; the report says so.
func TestAttachmentsOfTheOldPageAreCountedInTheReport(t *testing.T) {
	db := newDB(t)
	exec(t, db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
	history := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "what is this?"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
		}, "files": []any{map[string]any{"name": "a.pdf", "type": "file"}, map[string]any{"name": "b.wav", "type": "audio"}}},
		map[string]any{"role": "assistant", "content": "a cat"},
	}
	conv := mustJSON(map[string]any{"name": "Pics", "model": "m", "createdAt": 1700000000000, "history": history})
	exec(t, db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'c', ?, 1)`, alice, conv)
	tg := newTargets()
	r := run(t, convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks(t))
	it, ok := find(r, convert.KindChat, "Pics")
	if !ok || it.Status != convert.StatusConverted || !strings.Contains(it.Reason, "3 attachments") {
		t.Fatalf("item = %+v", it)
	}
	for _, e := range tg.chats[alice][0].Entries {
		if strings.Contains(e.Text, "base64") {
			t.Fatalf("image data in the transcript: %q", e.Text)
		}
	}
}

// unreadableMarks cannot read what an item became, as a database that is
// down would not.
type unreadableMarks struct{ convert.Marks }

func (unreadableMarks) Ref(context.Context, string) (string, bool, error) {
	return "", false, errors.New("marks store is down")
}

func TestAnItemIsNotConvertedAgainWhenItsMarkCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pool.json"), mustJSON(map[string]any{
		alice + ":Docs": map[string]any{"name": alice + ":Docs", "model": "m", "system_prompt": "x"},
	}))
	tg := newTargets()
	marks := memMarks(t)
	src := convert.Sources{StateDir: dir, AuthEnabled: true}

	run(t, src, tg, marks) // the agent is made once
	if len(tg.agents[alice]) != 1 {
		t.Fatalf("first run made %d agents", len(tg.agents[alice]))
	}
	r := run(t, src, tg, unreadableMarks{marks})
	if len(tg.agents[alice]) != 1 {
		t.Fatalf("an unreadable mark made a second agent: %+v", tg.agents[alice])
	}
	if it, _ := find(r, convert.KindAgent, "Docs"); it.Status != convert.StatusFailed || !strings.Contains(it.Reason, "marks store is down") {
		t.Fatalf("item = %+v", it)
	}
}
