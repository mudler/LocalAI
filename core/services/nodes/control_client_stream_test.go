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

	It("hands each progress line to the caller, with its bytes, and decodes the reply line", func() {
		worker.on(workerctl.VerbBackendInstall, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"progress":{"n":1}}`+"\n")
			_, _ = io.WriteString(w, `{"progress":{"n":2}}`+"\n")
			_, _ = io.WriteString(w, `{"reply":{"success":true}}`+"\n")
		})
		var seen []string
		var reply workerctl.BackendInstallReply
		err := client.CallStreaming(context.Background(), node, workerctl.VerbBackendInstall, map[string]string{"x": "y"}, &reply,
			func(raw json.RawMessage) { seen = append(seen, string(raw)) })
		Expect(err).ToNot(HaveOccurred())
		Expect(seen).To(Equal([]string{`{"n":1}`, `{"n":2}`}))
		Expect(reply.Success).To(BeTrue())
	})

	It("reads a 503 as a failure of the worker, never as a missing route", func() {
		worker.on(workerctl.VerbBackendInstall, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "upstream failure", http.StatusServiceUnavailable)
		})
		err := client.CallStreaming(context.Background(), node, workerctl.VerbBackendInstall, struct{}{}, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
	})

	It("keeps the budget of the caller ahead of a slow answer", func() {
		worker.on(workerctl.VerbBackendInstall, func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(60 * time.Millisecond)
			http.Error(w, "late", http.StatusServiceUnavailable)
		})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := client.CallStreaming(ctx, node, workerctl.VerbBackendInstall, struct{}{}, nil, nil)
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
	})
})
