package triggers

import (
	"strings"
	"testing"
	"time"
)

func goodSchedule() Trigger {
	return Trigger{ID: "t", Owner: "1", Name: "Morning", Prompt: "Summarise.", Kind: KindSchedule, Schedule: "daily 08:00", TZ: "Europe/Rome"}
}

func goodFile() Trigger {
	return Trigger{ID: "t", Owner: "1", Name: "Contracts", Prompt: "Read it.", Kind: KindFile, Folder: "/users/1/Contracts", Quiet: time.Minute}
}

func TestValidateAcceptsGoodTriggers(t *testing.T) {
	for _, tr := range []Trigger{goodSchedule(), goodFile()} {
		if err := tr.Validate(); err != nil {
			t.Errorf("%s trigger: %v", tr.Kind, err)
		}
	}
	tr := goodSchedule()
	tr.TZ = ""
	if err := tr.Validate(); err != nil {
		t.Errorf("an empty time zone is UTC: %v", err)
	}
	file := goodFile()
	file.TZ = "Europe/Rome"
	if err := file.Validate(); err != nil {
		t.Errorf("a file trigger with a time zone: %v", err)
	}
	file.TZ = "Mars/Base"
	if err := file.Validate(); err == nil || !strings.Contains(err.Error(), `time zone "Mars/Base" is not known`) {
		t.Errorf("a file trigger with a bad time zone: %v", err)
	}
}

func TestValidateNamesTheField(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*Trigger)
		want string
	}{
		{"no owner", func(t *Trigger) { t.Owner = "" }, "owner is required"},
		{"no name", func(t *Trigger) { t.Name = "  " }, "name is required"},
		{"long name", func(t *Trigger) { t.Name = strings.Repeat("é", 81) }, "name can have at most 80 characters"},
		{"no prompt", func(t *Trigger) { t.Prompt = "" }, "prompt is required"},
		{"long prompt", func(t *Trigger) { t.Prompt = strings.Repeat("p", 8001) }, "prompt can have at most 8000 bytes"},
		{"negative cap", func(t *Trigger) { t.DailyCap = -1 }, "daily limit must be 0 to 1000"},
		{"big cap", func(t *Trigger) { t.DailyCap = 1001 }, "daily limit must be 0 to 1000"},
		{"bad kind", func(t *Trigger) { t.Kind = "webhook" }, "kind must be"},
		{"bad schedule", func(t *Trigger) { t.Schedule = "daily 25:00" }, "hour must be 0 to 23"},
		{"bad zone", func(t *Trigger) { t.TZ = "Mars/Base" }, `time zone "Mars/Base" is not known`},
		{"long agent", func(t *Trigger) { t.AgentID = strings.Repeat("a", 201) }, "agent can have at most 200 characters"},
		{"long model", func(t *Trigger) { t.Model = strings.Repeat("m", 201) }, "model can have at most 200 characters"},
		{"long source chat", func(t *Trigger) { t.SourceChatID = strings.Repeat("c", 201) }, "source chat can have at most 200 characters"},
	} {
		tr := goodSchedule()
		c.edit(&tr)
		if err := tr.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	for _, c := range []struct {
		name string
		edit func(*Trigger)
	}{
		{"no folder", func(t *Trigger) { t.Folder = "" }},
		{"relative folder", func(t *Trigger) { t.Folder = "Contracts" }},
		{"unclean folder", func(t *Trigger) { t.Folder = "/a/../b" }},
		{"trailing slash", func(t *Trigger) { t.Folder = "/a/b/" }},
		{"the whole workspace", func(t *Trigger) { t.Folder = "/" }},
		{"negative quiet", func(t *Trigger) { t.Quiet = -time.Second }},
		{"huge quiet", func(t *Trigger) { t.Quiet = 25 * time.Hour }},
	} {
		tr := goodFile()
		c.edit(&tr)
		if err := tr.Validate(); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
	}
}

func TestValidateRefusesControlCharacters(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*Trigger)
		ok   bool
	}{
		{"NUL in name", func(t *Trigger) { t.Name = "a\x00b" }, false},
		{"escape in name", func(t *Trigger) { t.Name = "a\x1bb" }, false},
		{"newline in name", func(t *Trigger) { t.Name = "a\nb" }, false},
		{"tab in name", func(t *Trigger) { t.Name = "a\tb" }, true},
		{"NUL in prompt", func(t *Trigger) { t.Prompt = "do\x00it" }, false},
		{"bell in prompt", func(t *Trigger) { t.Prompt = "do\x07it" }, false},
		{"DEL in prompt", func(t *Trigger) { t.Prompt = "do\x7fit" }, false},
		{"newline and tab in prompt", func(t *Trigger) { t.Prompt = "line\n\tmore\n" }, true},
		{"CRLF in prompt", func(t *Trigger) { t.Prompt = "line\r\nmore\r\n" }, true},
		{"CR in name", func(t *Trigger) { t.Name = "a\rb" }, false},
	} {
		tr := goodSchedule()
		c.edit(&tr)
		if err := tr.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: %v, want ok=%v", c.name, err, c.ok)
		}
	}
	for _, c := range []struct {
		name   string
		folder string
		ok     bool
	}{
		{"NUL in folder", "/users/1/a\x00b", false},
		{"newline in folder", "/users/1/a\nb", false},
		{"escape in folder", "/users/1/a\x1bb", false},
		{"tab in folder", "/users/1/a\tb", true},
		{"plain folder", "/users/1/a b", true},
	} {
		tr := goodFile()
		tr.Folder = c.folder
		if err := tr.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestDefaults(t *testing.T) {
	var tr Trigger
	if tr.QuietOrDefault() != 30*time.Second || tr.CapOrDefault() != 24 {
		t.Fatalf("defaults %v %d", tr.QuietOrDefault(), tr.CapOrDefault())
	}
	tr.Quiet, tr.DailyCap = time.Minute, 3
	if tr.QuietOrDefault() != time.Minute || tr.CapOrDefault() != 3 {
		t.Fatal("explicit values were replaced")
	}
}

func TestArm(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.FixedZone("x", 3600))
	earlier := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	old := goodSchedule()
	old.Enabled, old.Since = true, earlier
	same := old
	if got := Arm(&old, same, now); !got.Since.Equal(earlier) {
		t.Errorf("an unchanged trigger got Since %v", got.Since)
	}
	renamed := old
	renamed.Name, renamed.Prompt = "Other", "Other prompt"
	if got := Arm(&old, renamed, now); !got.Since.Equal(earlier) {
		t.Errorf("a rename moved Since to %v", got.Since)
	}
	for _, edit := range []func(*Trigger){
		func(t *Trigger) { t.Schedule = "daily 09:00" },
		func(t *Trigger) { t.TZ = "UTC" },
		func(t *Trigger) { t.Kind, t.Folder = KindFile, "/a/b" },
	} {
		changed := old
		edit(&changed)
		if got := Arm(&old, changed, now); !got.Since.Equal(now) || got.Since.Location() != time.UTC {
			t.Errorf("a changed trigger got Since %v", got.Since)
		}
	}
	file := goodFile()
	file.Enabled, file.Since = true, earlier
	moved := file
	moved.Folder = "/a/other"
	if got := Arm(&file, moved, now); !got.Since.Equal(now) {
		t.Errorf("a new folder kept Since %v", got.Since)
	}
	off := old
	off.Enabled = false
	on := off
	on.Enabled = true
	if got := Arm(&off, on, now); !got.Since.Equal(now) {
		t.Errorf("switching on kept Since %v", got.Since)
	}
	if got := Arm(&old, off, now); !got.Since.Equal(earlier) {
		t.Errorf("switching off moved Since to %v", got.Since)
	}
	if got := Arm(nil, same, now); !got.Since.Equal(now) {
		t.Errorf("a new trigger got Since %v", got.Since)
	}
}

func TestCutReason(t *testing.T) {
	if got := CutReason("  short\n"); got != "short" {
		t.Errorf("CutReason = %q", got)
	}
	long := strings.Repeat("ü", 250)
	if got := CutReason(long); len([]rune(got)) != 200 || !strings.HasPrefix(long, got) {
		t.Errorf("long reason: %d runes", len([]rune(got)))
	}
}

func TestStatusFinal(t *testing.T) {
	for st, want := range map[Status]bool{StatusQueued: false, StatusRunning: false, StatusDone: true, StatusFailed: true, StatusSkipped: true} {
		if st.Final() != want {
			t.Errorf("%s.Final() = %v", st, st.Final())
		}
	}
}
