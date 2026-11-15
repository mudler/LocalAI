package studio_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var usualCaps = []string{"files.upload", "loops.wait", "memory.write", "proposals.confirm", "talkback.channels", "triggers.propose", "triggers.run"}

func migrationKinds() []studio.ConnectorKind {
	perms := []string{"memory.write", "files.upload", "triggers.run"}
	return []studio.ConnectorKind{
		{AddonID: "a-chat", ConnectorKind: contract.ConnectorKind{ID: "chat", Permissions: perms}},
		{AddonID: "a-bare", ConnectorKind: contract.ConnectorKind{ID: "bare"}},
	}
}

func putBinding(t *testing.T, st connectors.Store, b connectors.Binding) {
	t.Helper()
	if b.Name == "" {
		b.Name = "Bot " + b.ID
	}
	if b.AgentID == "" {
		b.AgentID = "ag1"
	}
	if err := st.PutBinding(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

func capsOf(t *testing.T, st connectors.Store, id string) []string {
	t.Helper()
	b, err := st.GetBinding(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return b.Caps
}

func TestUsualSetIsWhatThePresetGrants(t *testing.T) {
	got := contract.UsualConnectorCapabilities()
	if !slices.Equal(got, usualCaps) {
		t.Fatalf("usual set = %v, want %v", got, usualCaps)
	}
	for _, c := range got {
		if contract.SensitiveCapability(c) {
			t.Fatalf("sensitive capability %s is in the usual set", c)
		}
	}
	// The preset button turns on the capabilities that the list marks usual.
	var ui []string
	for _, c := range contract.ConnectorCapabilities() {
		if c.Usual && !c.Sensitive {
			ui = append(ui, c.Name)
		}
	}
	if !slices.Equal(ui, got) {
		t.Fatalf("the settings list marks %v usual, the migration grants %v", ui, got)
	}
}

func TestMigrateBindingCapsTable(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		b    connectors.Binding
		want []string
	}{
		{"never set, kind with permissions", connectors.Binding{ID: "old", Kind: "chat", AddonID: "a-chat"}, usualCaps},
		{"never set, no add-on recorded", connectors.Binding{ID: "noaddon", Kind: "chat"}, usualCaps},
		{"already chose some", connectors.Binding{ID: "some", Kind: "chat", AddonID: "a-chat", Caps: []string{"memory.write"}}, []string{"memory.write"}},
		{"already chose a sensitive one", connectors.Binding{ID: "sens", Kind: "chat", AddonID: "a-chat", Caps: []string{"profile.write"}}, []string{"profile.write"}},
		{"kind declares no permissions", connectors.Binding{ID: "bare", Kind: "bare", AddonID: "a-bare"}, nil},
		{"kind not running", connectors.Binding{ID: "gone", Kind: "other", AddonID: "a-other"}, nil},
		{"other add-on serves that kind id", connectors.Binding{ID: "hijack", Kind: "chat", AddonID: "a-other"}, nil},
		{"no kind", connectors.Binding{ID: "nokind"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := connectors.NewJSON("")
			tc.b.Kind = cmpOr(tc.b.Kind, "chat")
			if tc.b.ID == "nokind" {
				// Validate refuses a binding without a kind, but an old row can have none.
				tc.b.Kind = ""
			}
			putBinding(t, st, tc.b)
			rep, err := studio.MigrateBindingCaps(context.Background(), st, migrationKinds(), now, nil)
			if err != nil || !rep.Ran {
				t.Fatalf("rep=%+v err=%v", rep, err)
			}
			if got := capsOf(t, st, tc.b.ID); !slices.Equal(got, tc.want) {
				t.Fatalf("caps = %v, want %v", got, tc.want)
			}
		})
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestMigrateBindingCapsRunsOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	st, _ := connectors.NewJSON("")
	putBinding(t, st, connectors.Binding{ID: "b1", Kind: "chat", AddonID: "a-chat"})
	rep, err := studio.MigrateBindingCaps(ctx, st, migrationKinds(), now, nil)
	if err != nil || !rep.Ran || rep.Changed != 1 {
		t.Fatalf("first run: %+v %v", rep, err)
	}
	// The owner clears every switch on purpose, and a new empty binding is made.
	b, _ := st.GetBinding(ctx, "b1")
	b.Caps = nil
	putBinding(t, st, b)
	putBinding(t, st, connectors.Binding{ID: "b2", Kind: "chat", AddonID: "a-chat"})
	for i := range 3 {
		rep, err = studio.MigrateBindingCaps(ctx, st, migrationKinds(), now.Add(time.Duration(i+1)*24*time.Hour), nil)
		if err != nil || rep.Ran || rep.Changed != 0 {
			t.Fatalf("run %d: %+v %v", i+2, rep, err)
		}
	}
	if len(capsOf(t, st, "b1")) != 0 || len(capsOf(t, st, "b2")) != 0 {
		t.Fatal("a binding that was cleared or made after the migration got permissions")
	}
}

func TestMigrateBindingCapsConcurrent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	st, _ := connectors.NewJSON("")
	for _, id := range []string{"a", "b", "c"} {
		putBinding(t, st, connectors.Binding{ID: id, Kind: "chat", AddonID: "a-chat"})
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ran, changed := 0, 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rep, err := studio.MigrateBindingCaps(ctx, st, migrationKinds(), now, nil)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if rep.Ran {
				ran++
			}
			changed += rep.Changed
		}()
	}
	wg.Wait()
	if ran != 1 || changed != 3 {
		t.Fatalf("ran=%d changed=%d, want 1 and 3", ran, changed)
	}
	for _, id := range []string{"a", "b", "c"} {
		if !slices.Equal(capsOf(t, st, id), usualCaps) {
			t.Fatalf("binding %s has %v", id, capsOf(t, st, id))
		}
	}
}

// failingStore makes the write of one binding fail, to show that a failed run
// releases its claim and a later run finishes the job.
type failingStore struct {
	connectors.Store
	failOn string
	fail   bool
}

func (f *failingStore) SetCapsIfEmpty(ctx context.Context, id string, caps []string) (bool, error) {
	if f.fail && id == f.failOn {
		return false, errors.New("disk full")
	}
	return f.Store.SetCapsIfEmpty(ctx, id, caps)
}

func TestMigrateBindingCapsRetriesAfterAFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	inner, _ := connectors.NewJSON("")
	st := &failingStore{Store: inner, failOn: "b", fail: true}
	for _, id := range []string{"a", "b"} {
		putBinding(t, st, connectors.Binding{ID: id, Kind: "chat", AddonID: "a-chat"})
	}
	if _, err := studio.MigrateBindingCaps(ctx, st, migrationKinds(), now, nil); err == nil {
		t.Fatal("expected the failure")
	}
	st.fail = false
	rep, err := studio.MigrateBindingCaps(ctx, st, migrationKinds(), now.Add(time.Second), nil)
	if err != nil || !rep.Ran || rep.Changed != 1 {
		t.Fatalf("retry: %+v %v (the first binding was done already)", rep, err)
	}
	if !slices.Equal(capsOf(t, st, "b"), usualCaps) {
		t.Fatal("the failed binding was not migrated on retry")
	}
}

func TestMigrateBindingCapsNeedsKinds(t *testing.T) {
	st, _ := connectors.NewJSON("")
	putBinding(t, st, connectors.Binding{ID: "b1", Kind: "chat", AddonID: "a-chat"})
	if _, err := studio.MigrateBindingCaps(context.Background(), st, nil, time.Now(), nil); err == nil {
		t.Fatal("with no kinds the migration must not run, and must not be marked done")
	}
	rep, err := studio.MigrateBindingCaps(context.Background(), st, migrationKinds(), time.Now(), nil)
	if err != nil || !rep.Ran || rep.Changed != 1 {
		t.Fatalf("later run: %+v %v", rep, err)
	}
}

func TestMigrateBindingCapsLogsWithoutSecrets(t *testing.T) {
	var buf lockedBuffer
	log := newTextLogger(&buf)
	st, _ := connectors.NewJSON("")
	putBinding(t, st, connectors.Binding{ID: "b1", Kind: "chat", AddonID: "a-chat", Token: []byte("sealed-secret-token"), Allow: []string{"123"}})
	if _, err := studio.MigrateBindingCaps(context.Background(), st, migrationKinds(), time.Now(), log); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"binding=b1", "kind=chat", "memory.write", "changed=1"} {
		if !hasText(out, want) {
			t.Fatalf("log lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"sealed-secret-token", "123", "profile.write"} {
		if hasText(out, bad) {
			t.Fatalf("log has %q:\n%s", bad, out)
		}
	}
}

func TestMigrateLoopsWaitAddsItOnceAndKeepsAnOwnerChoice(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	st, _ := connectors.NewJSON("")
	// A binding that ran the usual-set migration before loops.wait existed, one
	// the owner narrowed, one with no ceiling, and one that has it already.
	putBinding(t, st, connectors.Binding{ID: "old", Kind: "chat", Caps: []string{"memory.write", "proposals.confirm"}})
	putBinding(t, st, connectors.Binding{ID: "narrow", Kind: "chat", Caps: []string{"files.upload"}})
	putBinding(t, st, connectors.Binding{ID: "none", Kind: "chat"})
	putBinding(t, st, connectors.Binding{ID: "has", Kind: "chat", Caps: []string{"loops.wait"}})
	rep, err := studio.MigrateLoopsWait(ctx, st, now, nil)
	if err != nil || !rep.Ran || rep.Changed != 2 {
		t.Fatalf("first run: %+v %v", rep, err)
	}
	if !slices.Contains(capsOf(t, st, "old"), "loops.wait") || !slices.Contains(capsOf(t, st, "narrow"), "loops.wait") {
		t.Fatal("an existing binding did not get loops.wait")
	}
	if len(capsOf(t, st, "none")) != 0 {
		t.Fatal("a binding with an empty ceiling must stay empty")
	}
	if slices.Contains(capsOf(t, st, "old"), "loops.start") {
		t.Fatal("loops.start must never come from a migration")
	}
	// The owner switches loops.wait off. The migration does not run again.
	b, _ := st.GetBinding(ctx, "old")
	b.Caps = []string{"memory.write", "proposals.confirm"}
	putBinding(t, st, b)
	for i := range 3 {
		rep, err = studio.MigrateLoopsWait(ctx, st, now.Add(time.Duration(i+1)*time.Hour), nil)
		if err != nil || rep.Ran || rep.Changed != 0 {
			t.Fatalf("run %d: %+v %v", i+2, rep, err)
		}
	}
	if slices.Contains(capsOf(t, st, "old"), "loops.wait") {
		t.Fatal("the migration turned loops.wait back on after the owner switched it off")
	}
}
