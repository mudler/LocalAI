package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("The control client and the verbs of an agent worker", func() {
	const node = "11111111-2222-3333-4444-555555555555"

	var (
		worker *controlWorker
		client *ControlClient
	)

	BeforeEach(func() {
		worker = newControlWorker()
		client = NewControlClient(worker.dialerFor(), "registration-token")
	})

	It("hands the subject of each progress line to the caller, with its bytes", func() {
		worker.on(workerctl.VerbAgentExecute, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"subject":"jobs.j1.progress","progress":{"n":1}}`+"\n")
			_, _ = io.WriteString(w, `{"progress":{"n":2}}`+"\n")
			_, _ = io.WriteString(w, `{"reply":{"job_id":"j1","status":"completed"}}`+"\n")
		})
		type line struct {
			subject string
			raw     string
		}
		var seen []line
		var reply workerctl.RunReply
		err := client.CallStreaming(context.Background(), node, workerctl.VerbAgentExecute, map[string]string{"x": "y"}, &reply,
			func(subject string, raw json.RawMessage) { seen = append(seen, line{subject, string(raw)}) })
		Expect(err).ToNot(HaveOccurred())
		Expect(seen).To(Equal([]line{{"jobs.j1.progress", `{"n":1}`}, {"", `{"n":2}`}}))
		Expect(reply).To(Equal(workerctl.RunReply{JobID: "j1", Status: "completed"}))
	})

	It("tells a worker that has no free slot from a worker that failed", func() {
		worker.on(workerctl.VerbMCPCIRun, func(w http.ResponseWriter, _ *http.Request) {
			workerctl.WriteBusy(w)
		})
		err := client.CallStreaming(context.Background(), node, workerctl.VerbMCPCIRun, struct{}{}, nil, nil)
		Expect(err).To(MatchError(ErrWorkerBusy))
		Expect(errors.Is(err, ErrNoRoute)).To(BeFalse(), "a busy worker is present and routable")

		worker.on(workerctl.VerbMCPCIRun, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "upstream failure", http.StatusServiceUnavailable)
		})
		err = client.CallStreaming(context.Background(), node, workerctl.VerbMCPCIRun, struct{}{}, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrWorkerBusy)).To(BeFalse(), "only the answer of the worker counts")
	})

	It("keeps the budget of the caller ahead of the answer of a busy worker", func() {
		worker.on(workerctl.VerbMCPCIRun, func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(60 * time.Millisecond)
			workerctl.WriteBusy(w)
		})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := client.CallStreaming(ctx, node, workerctl.VerbMCPCIRun, struct{}{}, nil, nil)
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(errors.Is(err, ErrWorkerBusy)).To(BeFalse())
	})
})
