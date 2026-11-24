package p2p_test

import (
	"context"
	"net/http"

	. "github.com/mudler/LocalAI/core/http/endpoints/p2p"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeSplit struct {
	err error
	id  string
	req SplitWorkerUpdate
}

func (f *fakeSplit) ListSplitWorkers(context.Context) (SplitWorkerList, error) {
	return SplitWorkerList{Workers: []SplitWorkerView{{ID: "w1", Engine: "llama", State: "slow"}}, MemoryInUse: 0}, f.err
}

func (f *fakeSplit) SetSplitWorker(_ context.Context, id string, req SplitWorkerUpdate) (SplitWorkerView, error) {
	f.id, f.req = id, req
	return SplitWorkerView{ID: id, State: "online", AllowSlowPath: *req.AllowSlowPath}, f.err
}

var _ = Describe("model-split handlers", func() {
	var svc *fakeSplit
	BeforeEach(func() { svc = &fakeSplit{} })

	It("lists the workers", func() {
		rec := call(ListSplitWorkersEndpoint(svc), http.MethodGet, "", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"state":"slow"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"memory_in_use":0`))
	})

	It("sets the slow-path choice of a worker", func() {
		rec := call(SetSplitWorkerEndpoint(svc), http.MethodPatch, `{"allow_slow_path":true}`, nil, "id", "w1")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(svc.id).To(Equal("w1"))
		Expect(*svc.req.AllowSlowPath).To(BeTrue())
		Expect(rec.Body.String()).To(ContainSubstring(`"allow_slow_path":true`))
	})

	It("refuses a change that names nothing", func() {
		rec := call(SetSplitWorkerEndpoint(svc), http.MethodPatch, `{}`, nil, "id", "w1")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(errorOf(rec)).To(ContainSubstring("allow_slow_path"))
	})

	It("answers 404 and 409 from the service", func() {
		svc.err = ErrNotFound
		rec := call(SetSplitWorkerEndpoint(svc), http.MethodPatch, `{"allow_slow_path":false}`, nil, "id", "nope")
		Expect(rec.Code).To(Equal(http.StatusNotFound))
		svc.err = &ModeError{Mode: "connect", Need: "share"}
		rec = call(ListSplitWorkersEndpoint(svc), http.MethodGet, "", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
	})
})
