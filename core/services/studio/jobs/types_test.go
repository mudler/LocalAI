package jobs_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/jobs"
)

var _ = Describe("task validation", func() {
	good := jobs.Task{ID: "t", Owner: "u", Name: "N", Model: "m", Prompt: "Say {{.word}}"}

	BeforeEach(func() {
		Expect(jobs.Validate(good)).To(Succeed())
	})

	DescribeTable("refuses with the reason",
		func(mutate func(*jobs.Task), want string) {
			task := good
			mutate(&task)
			err := jobs.Validate(task)
			Expect(err).To(MatchError(jobs.ErrInvalid))
			Expect(err.Error()).To(ContainSubstring(want))
		},
		Entry("no name", func(t *jobs.Task) { t.Name = " " }, "name is required"),
		Entry("no prompt", func(t *jobs.Task) { t.Prompt = "" }, "prompt is required"),
		Entry("no model and no agent", func(t *jobs.Task) { t.Model = "" }, "model is required"),
		Entry("bad template", func(t *jobs.Task) { t.Prompt = "{{.word" }, "not a valid template"),
		Entry("webhook without url", func(t *jobs.Task) { t.Webhooks = []jobs.Webhook{{}} }, "no URL"),
		Entry("webhook bad method", func(t *jobs.Task) { t.Webhooks = []jobs.Webhook{{URL: "https://a.test", Method: "GET"}} }, "POST, PUT or PATCH"),
		Entry("webhook bad payload", func(t *jobs.Task) {
			t.Webhooks = []jobs.Webhook{{URL: "https://a.test", PayloadTemplate: "{{"}}
		}, "payload template is not valid"),
		Entry("too many webhooks", func(t *jobs.Task) { t.Webhooks = make([]jobs.Webhook, jobs.MaxWebhooks+1) }, "at most"),
	)
})

var _ = Describe("payload template functions", func() {
	It("keeps the server environment out", func() {
		f := jobs.PayloadFuncs()
		for _, name := range []string{"env", "expandenv", "getHostByName"} {
			Expect(f).NotTo(HaveKey(name), "%s is available to a payload template", name)
		}
		for _, name := range []string{"json", "upper", "default"} {
			Expect(f).To(HaveKey(name), "%s is missing", name)
		}
	})
})
