package studio

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("a background job in the API", func() {
	It("gives state, end reason, label and age, and nothing about where or what it runs", func() {
		at := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
		v := studiosvc.JobView{Chat: "c1", Total: 5300, LastOutput: at.Add(time.Minute), JobStub: session.JobStub{
			ID: "job-ab12cd", Instance: "node-a", Label: "tests", State: bgjob.StateExited, End: bgjob.EndExit, Exit: 1, Started: at, Ended: at.Add(time.Hour)}}
		out := jobJSON(v)
		Expect(out.Provider).To(Equal("builtin"))
		Expect(out.State).To(Equal("exited"))
		Expect(out.Exit).To(Equal(1))
		Expect(out.Bytes).To(BeNumerically("==", 5300))
		raw, err := json.Marshal(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("node-a"))
		Expect(string(raw)).NotTo(ContainSubstring("command"))
	})
})
