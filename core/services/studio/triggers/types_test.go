package triggers

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func goodSchedule() Trigger {
	return Trigger{ID: "t", Owner: "1", Name: "Morning", Prompt: "Summarise.", Kind: KindSchedule, Schedule: "daily 08:00", TZ: "Europe/Rome"}
}

func goodFile() Trigger {
	return Trigger{ID: "t", Owner: "1", Name: "Contracts", Prompt: "Read it.", Kind: KindFile, Folder: "/users/1/Contracts", Quiet: time.Minute}
}

var _ = Describe("Trigger types", func() {
	It("accepts good triggers on validation", func() {
		for _, tr := range []Trigger{goodSchedule(), goodFile()} {
			Expect(tr.Validate()).To(Succeed(), "%s trigger", tr.Kind)
		}
		tr := goodSchedule()
		tr.TZ = ""
		Expect(tr.Validate()).To(Succeed(), "an empty time zone is UTC")
		file := goodFile()
		file.TZ = "Europe/Rome"
		Expect(file.Validate()).To(Succeed(), "a file trigger with a time zone")
		file.TZ = "Mars/Base"
		err := file.Validate()
		Expect(err).To(HaveOccurred(), "a file trigger with a bad time zone")
		Expect(err.Error()).To(ContainSubstring(`time zone "Mars/Base" is not known`))
	})

	It("keeps a plan and a gap", func() {
		tr := goodFile()
		tr.Reads, tr.Writes, tr.Widget, tr.EstTokens, tr.MinGap = "Mail from the last day", "inbox/summary.json", "inbox", 1500, time.Hour
		Expect(tr.Validate()).To(Succeed())
		Expect(tr.GapOrDefault()).To(Equal(time.Hour))
		Expect(goodFile().GapOrDefault()).To(Equal(DefaultFileGap))
	})

	It("names the field on a validation error", func() {
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
			{"negative gap", func(t *Trigger) { t.MinGap = -time.Second }, "the time between runs must be 0 to"},
			{"huge gap", func(t *Trigger) { t.MinGap = 25 * time.Hour }, "the time between runs must be 0 to"},
			{"long reads", func(t *Trigger) { t.Reads = strings.Repeat("r", 201) }, "reads is one line"},
			{"reads with a line break", func(t *Trigger) { t.Reads = "mail\nand more" }, "reads is one line"},
			{"long writes", func(t *Trigger) { t.Writes = "/" + strings.Repeat("w", 256) }, "writes is a path"},
			{"bad widget", func(t *Trigger) { t.Widget = "Inbox" }, "widget is the id of a Home widget"},
			{"negative tokens", func(t *Trigger) { t.EstTokens = -1 }, "estimated tokens must be"},
		} {
			tr := goodSchedule()
			c.edit(&tr)
			err := tr.Validate()
			Expect(err).To(HaveOccurred(), c.name)
			Expect(err.Error()).To(ContainSubstring(c.want), c.name)
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
			Expect(tr.Validate()).NotTo(Succeed(), "%s was accepted", c.name)
		}
	})

	It("refuses control characters on validation", func() {
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
			if c.ok {
				Expect(tr.Validate()).To(Succeed(), c.name)
			} else {
				Expect(tr.Validate()).NotTo(Succeed(), c.name)
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
			if c.ok {
				Expect(tr.Validate()).To(Succeed(), c.name)
			} else {
				Expect(tr.Validate()).NotTo(Succeed(), c.name)
			}
		}
	})

	It("has defaults", func() {
		var tr Trigger
		Expect(tr.QuietOrDefault()).To(Equal(30 * time.Second))
		Expect(tr.CapOrDefault()).To(Equal(24))
		tr.Quiet, tr.DailyCap = time.Minute, 3
		Expect(tr.QuietOrDefault()).To(Equal(time.Minute), "explicit values were replaced")
		Expect(tr.CapOrDefault()).To(Equal(3), "explicit values were replaced")
	})

	It("arms a trigger", func() {
		now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.FixedZone("x", 3600))
		earlier := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		old := goodSchedule()
		old.Enabled, old.Since = true, earlier
		same := old
		Expect(Arm(&old, same, now).Since.Equal(earlier)).To(BeTrue(), "an unchanged trigger moved Since")
		renamed := old
		renamed.Name, renamed.Prompt = "Other", "Other prompt"
		Expect(Arm(&old, renamed, now).Since.Equal(earlier)).To(BeTrue(), "a rename moved Since")
		for _, edit := range []func(*Trigger){
			func(t *Trigger) { t.Schedule = "daily 09:00" },
			func(t *Trigger) { t.TZ = "UTC" },
			func(t *Trigger) { t.Kind, t.Folder = KindFile, "/a/b" },
		} {
			changed := old
			edit(&changed)
			got := Arm(&old, changed, now)
			Expect(got.Since.Equal(now)).To(BeTrue(), "a changed trigger got Since %v", got.Since)
			Expect(got.Since.Location()).To(Equal(time.UTC), "a changed trigger got Since %v", got.Since)
		}
		file := goodFile()
		file.Enabled, file.Since = true, earlier
		moved := file
		moved.Folder = "/a/other"
		Expect(Arm(&file, moved, now).Since.Equal(now)).To(BeTrue(), "a new folder kept Since")
		off := old
		off.Enabled = false
		on := off
		on.Enabled = true
		Expect(Arm(&off, on, now).Since.Equal(now)).To(BeTrue(), "switching on kept Since")
		Expect(Arm(&old, off, now).Since.Equal(earlier)).To(BeTrue(), "switching off moved Since")
		Expect(Arm(nil, same, now).Since.Equal(now)).To(BeTrue(), "a new trigger got a wrong Since")
	})

	It("cuts a reason", func() {
		Expect(CutReason("  short\n")).To(Equal("short"))
		long := strings.Repeat("ü", 250)
		got := CutReason(long)
		Expect([]rune(got)).To(HaveLen(200), "long reason")
		Expect(long).To(HavePrefix(got), "long reason")
	})

	It("tells which statuses are final", func() {
		for st, want := range map[Status]bool{StatusQueued: false, StatusRunning: false, StatusDone: true, StatusFailed: true, StatusSkipped: true} {
			Expect(st.Final()).To(Equal(want), "%s.Final()", st)
		}
	})
})
