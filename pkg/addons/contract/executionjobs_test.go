// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// jobsFixture is the extension fixture with a jobs tool bound to the provider.
func jobsFixture() []byte {
	b := string(extensionFixture())
	b = strings.Replace(b, `"tools":["run","health"]`, `"tools":["run","health","jobs"]`, 1)
	b = strings.Replace(b, `{"name":"health",`, `{"name":"jobs","description":"Background jobs","kind":"action","uiOnly":true,"inputSchema":{"type":"object","properties":{"verb":{"type":"string"}},"required":["verb"],"additionalProperties":false}},{"name":"health",`, 1)
	b = strings.Replace(b, `"statusTool":"health"}`, `"statusTool":"health","jobs":{"tool":"jobs"}}`, 1)
	return []byte(strings.Replace(b, `"schemaVersion":1`, `"schemaVersion":1,"featureLevel":6`, 1))
}

var _ = Describe("Execution jobs", func() {
	It("parses a provider with a jobs tool at feature level 6 and keeps it through an echo", func() {
		m, err := ParseManifest(jobsFixture())
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Capabilities.ExecutionProvider.Jobs).To(Equal(&ExecutionJobs{Tool: "jobs"}))
		b, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		again, err := ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(m))
	})

	It("needs featureLevel 6, and a level 5 host says it needs a newer LocalAI", func() {
		m, err := ParseManifest(jobsFixture())
		Expect(err).NotTo(HaveOccurred())
		Expect(HostFeatureLevel).To(BeNumerically(">=", 6))
		h := compatible()
		h.SDK = HostSDKVersion // the fixture has a page
		Expect(ValidateManifest(m, h)).To(Succeed())
		h.FeatureLevel = 5
		err = ValidateManifest(m, h)
		Expect(err).To(MatchError("needs a newer LocalAI"))
		var refusal *FeatureLevelRefusal
		Expect(err).To(BeAssignableToTypeOf(refusal))
		m.FeatureLevel = 5
		h.FeatureLevel = HostFeatureLevel
		Expect(ValidateManifest(m, h)).To(MatchError(ContainSubstring("must declare featureLevel 6")))
	})

	It("is refused by the strict reader of an older host, which knows no jobs key", func() {
		type olderProvider struct {
			Tools      []string `json:"tools"`
			StatusTool string   `json:"statusTool"`
		}
		var o olderProvider
		Expect(strictJSON([]byte(`{"tools":["run"],"statusTool":"health","jobs":{"tool":"jobs"}}`), &o, 1024)).NotTo(Succeed())
	})

	It("refuses an unknown key in the jobs binding", func() {
		b := strings.Replace(string(jobsFixture()), `"jobs":{"tool":"jobs"}`, `"jobs":{"tool":"jobs","extra":1}`, 1)
		_, err := ParseManifest([]byte(b))
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("binds only a declared page-only action tool", func(change func(*Capabilities), message string) {
		m, err := ParseManifest(jobsFixture())
		Expect(err).NotTo(HaveOccurred())
		change(&m.Capabilities)
		Expect(validateExtensions(m.Capabilities)).To(MatchError(message))
	},
		Entry("not uiOnly", func(c *Capabilities) { c.ToolDefinitions[1].UIOnly = false }, "execution jobs must bind a declared page-only action tool"),
		Entry("a read tool", func(c *Capabilities) { c.ToolDefinitions[1].Kind = ToolKindRead }, "execution jobs must bind a declared page-only action tool"),
		Entry("a shadow", func(c *Capabilities) { c.ToolDefinitions[1].Shadows = "bash" }, "execution jobs must bind a declared page-only action tool"),
		Entry("an admin tool", func(c *Capabilities) { c.AdminTools = []string{"jobs"} }, "execution jobs must bind a declared page-only action tool"),
		Entry("undeclared", func(c *Capabilities) { c.ExecutionProvider.Jobs.Tool = "other" }, "execution jobs must bind a declared page-only action tool"),
		Entry("the status tool", func(c *Capabilities) { c.ExecutionProvider.Jobs.Tool = "health" }, "invalid execution jobs binding"),
		Entry("an execution tool", func(c *Capabilities) { c.ExecutionProvider.Jobs.Tool = "run" }, "invalid execution jobs binding"),
		Entry("a bad name", func(c *Capabilities) { c.ExecutionProvider.Jobs.Tool = "../x" }, "invalid execution jobs binding"),
	)

	Describe("arguments", func() {
		since := func(n int64) *int64 { return &n }
		DescribeTable("validate per verb", func(a JobArgs, ok bool) {
			if ok {
				Expect(a.Validate()).To(Succeed())
			} else {
				Expect(a.Validate()).To(MatchError(ErrJobArgs))
			}
		},
			Entry("start", JobArgs{Verb: JobStart, Command: "make", Label: "build"}, true),
			Entry("start without command", JobArgs{Verb: JobStart, Command: "  "}, false),
			Entry("start with NUL", JobArgs{Verb: JobStart, Command: "a\x00b"}, false),
			Entry("start too long", JobArgs{Verb: JobStart, Command: strings.Repeat("a", MaxJobCommandBytes+1)}, false),
			Entry("start label with a newline", JobArgs{Verb: JobStart, Command: "x", Label: "a\nb"}, false),
			Entry("start label with a direction override", JobArgs{Verb: JobStart, Command: "x", Label: "a‮b"}, false),
			Entry("start label too long", JobArgs{Verb: JobStart, Command: "x", Label: strings.Repeat("é", MaxJobLabelChars+1)}, false),
			Entry("start with an id", JobArgs{Verb: JobStart, Command: "x", ID: "job-abcdef"}, false),
			Entry("list", JobArgs{Verb: JobList}, true),
			Entry("read", JobArgs{Verb: JobRead, ID: "job-abcdef", Since: since(10), MaxBytes: 300}, true),
			Entry("read without id", JobArgs{Verb: JobRead}, false),
			Entry("read with a traversal id", JobArgs{Verb: JobRead, ID: "../../etc"}, false),
			Entry("read with an upper case id", JobArgs{Verb: JobRead, ID: "job-ABCDEF"}, false),
			Entry("read negative since", JobArgs{Verb: JobRead, ID: "job-abcdef", Since: since(-1)}, false),
			Entry("read too much", JobArgs{Verb: JobRead, ID: "job-abcdef", MaxBytes: MaxJobReadBytes + 1}, false),
			Entry("read too little", JobArgs{Verb: JobRead, ID: "job-abcdef", MaxBytes: 1}, false),
			Entry("input", JobArgs{Verb: JobInput, ID: "job-abcdef", Input: "y\n"}, true),
			Entry("input close only", JobArgs{Verb: JobInput, ID: "job-abcdef", CloseInput: true}, true),
			Entry("input empty", JobArgs{Verb: JobInput, ID: "job-abcdef"}, false),
			Entry("input too long", JobArgs{Verb: JobInput, ID: "job-abcdef", Input: strings.Repeat("a", MaxJobInputBytes+1)}, false),
			Entry("input with a signal", JobArgs{Verb: JobInput, ID: "job-abcdef", Input: "a", Signal: "kill"}, false),
			Entry("kill", JobArgs{Verb: JobKill, ID: "job-abcdef", Signal: "int"}, true),
			Entry("kill with a bad signal", JobArgs{Verb: JobKill, ID: "job-abcdef", Signal: "hup"}, false),
			Entry("kill for a reason", JobArgs{Verb: JobKill, ID: "job-abcdef", Reason: "group_off"}, true),
			Entry("kill stopped by the person", JobArgs{Verb: JobKill, ID: "job-abcdef", Reason: "stopped"}, true),
			Entry("kill for a reason only the add-on gives", JobArgs{Verb: JobKill, ID: "job-abcdef", Reason: "lifetime"}, false),
			Entry("kill for an unknown reason", JobArgs{Verb: JobKill, ID: "job-abcdef", Reason: "because"}, false),
			Entry("read with a reason", JobArgs{Verb: JobRead, ID: "job-abcdef", Reason: "fenced"}, false),
			Entry("unknown verb", JobArgs{Verb: "exec"}, false),
		)
	})

	Describe("answers", func() {
		now := time.Now()
		running := JobInfo{ID: "job-abcdef", State: "running", Exit: -1, Started: now, LastOutput: now, Total: 100}
		done := JobInfo{ID: "job-bcdefg", State: "exited", End: "exit", Exit: 0, Started: now, Ended: now, LastOutput: now, Total: 3}
		read := JobArgs{Verb: JobRead, ID: "job-abcdef"}
		It("accepts a read, a list and a refusal", func() {
			Expect(JobReply{OK: true, Job: &running, Output: "hi", From: 98, Next: 100}.Validate(read)).To(Succeed())
			Expect(JobReply{OK: true, Jobs: []JobInfo{running, done}}.Validate(JobArgs{Verb: JobList})).To(Succeed())
			Expect(JobReply{Code: JobCodeUnknown, Error: "no such job"}.Validate(read)).To(Succeed())
		})
		It("accepts each end reason the host may ask for", func() {
			for _, r := range []string{"stopped", "chat_deleted", "agent_ended", "shell_off", "group_off", "fenced"} {
				j := done
				j.State, j.End = "exited", r
				Expect(JobReply{OK: true, Jobs: []JobInfo{j}}.Validate(JobArgs{Verb: JobList})).To(Succeed(), r)
			}
		})
		DescribeTable("refuses what a broken or hostile add-on answers", func(r JobReply, a JobArgs) {
			Expect(r.Validate(a)).To(MatchError(ErrJobReply))
		},
			Entry("huge output", JobReply{OK: true, Job: &running, Output: strings.Repeat("a", 3*MaxJobReadBytes+1), Next: 100}, read),
			Entry("a read past the total", JobReply{OK: true, Job: &running, From: 0, Next: 101}, read),
			Entry("a read larger than allowed", func() JobReply {
				j := running
				j.Total = 1 << 20
				return JobReply{OK: true, Job: &j, From: 0, Next: MaxJobReadBytes + 1}
			}(), read),
			Entry("offsets backwards", JobReply{OK: true, Job: &running, From: 50, Next: 40}, read),
			Entry("another job", JobReply{OK: true, Job: &done}, read),
			Entry("too many jobs", JobReply{OK: true, Jobs: make([]JobInfo, MaxJobsListed+1)}, JobArgs{Verb: JobList}),
			Entry("a duplicate job", JobReply{OK: true, Jobs: []JobInfo{running, running}}, JobArgs{Verb: JobList}),
			Entry("a bad id", JobReply{OK: true, Jobs: []JobInfo{{ID: "x", State: "running", Exit: -1}}}, JobArgs{Verb: JobList}),
			Entry("a bad state", func() JobReply { j := running; j.State = "zombie"; return JobReply{OK: true, Job: &j} }(), read),
			Entry("a finished job without an end time", func() JobReply { j := done; j.Ended = time.Time{}; return JobReply{OK: true, Jobs: []JobInfo{j}} }(), JobArgs{Verb: JobList}),
			Entry("an unknown code", JobReply{Code: "weird", Error: "x"}, read),
			Entry("a refusal with data", JobReply{Code: JobCodeUnknown, Error: "x", Job: &running}, read),
			Entry("a long refusal", JobReply{Code: JobCodeUnknown, Error: strings.Repeat("x", 2000)}, read),
			Entry("output on input", JobReply{OK: true, Job: &running, Output: "x"}, JobArgs{Verb: JobInput, ID: "job-abcdef", Input: "x"}),
			Entry("written too much", JobReply{OK: true, Job: &running, Written: MaxJobInputBytes + 1}, JobArgs{Verb: JobInput, ID: "job-abcdef", Input: "x"}),
		)
	})
})
