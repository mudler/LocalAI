package workerctl_test

import (
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Load recovery wire", func() {
	It("preserves identity and defaults legacy capabilities", func() {
		id := &workerctl.OperationIdentity{TrackingKey: "model", Generation: "g", Incarnation: "boot"}
		raw, err := json.Marshal(workerctl.ModelStopRequest{Operation: id, ProcessInstance: "instance"})
		Expect(err).NotTo(HaveOccurred())
		var got workerctl.ModelStopRequest
		Expect(json.Unmarshal(raw, &got)).To(Succeed())
		Expect(got.Operation).To(Equal(id))
		Expect(got.ProcessInstance).To(Equal("instance"))
		var legacy workerctl.ModelsRunningReply
		Expect(json.Unmarshal([]byte(`{"models":[]}`), &legacy)).To(Succeed())
		Expect(legacy.ReportsOperations).To(BeFalse())
		Expect(legacy.Incarnation).To(BeEmpty())
	})
})
