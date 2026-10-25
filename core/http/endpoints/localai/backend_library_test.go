package localai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeLibrary struct {
	err     error
	steps   []string
	updates []backendlib.Update
	last    [3]string
	block   chan struct{}
}

func (f *fakeLibrary) Install(_ context.Context, source, backend, version string, p backendlib.Progress) error {
	f.last = [3]string{source, backend, version}
	for _, s := range f.steps {
		p.Step(s)
		if s == backendlib.StepFetching {
			p.Bytes("layer", "1 MB", "2 MB", 50)
		}
	}
	if f.block != nil {
		<-f.block
	}
	return f.err
}

func (f *fakeLibrary) Updates(context.Context) ([]backendlib.Update, error) {
	return f.updates, nil
}

// fakeJobs keeps every status of every job, in order.
type fakeJobs struct {
	mu   sync.Mutex
	logs map[string][]galleryop.OpStatus
}

func (j *fakeJobs) UpdateStatus(id string, op *galleryop.OpStatus) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.logs == nil {
		j.logs = map[string][]galleryop.OpStatus{}
	}
	j.logs[id] = append(j.logs[id], *op)
}

func (j *fakeJobs) history(id string) []galleryop.OpStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]galleryop.OpStatus(nil), j.logs[id]...)
}

var allSteps = []string{backendlib.StepResolving, backendlib.StepVerifying, backendlib.StepSize, backendlib.StepFetching, backendlib.StepWriting}

var _ = Describe("install a backend from a library", func() {
	post := func(f *fakeLibrary, jobs *fakeJobs, body string) *httptest.ResponseRecorder {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/backends/library/install", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		Expect(localai.InstallBackendFromLibraryEndpoint(f, jobs)(e.NewContext(req, rec))).To(Succeed())
		return rec
	}
	jobID := func(rec *httptest.ResponseRecorder) string {
		var out struct {
			ID        string `json:"id"`
			StatusURL string `json:"status_url"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.ID).NotTo(BeEmpty())
		Expect(out.StatusURL).To(HaveSuffix("backends/jobs/" + out.ID))
		return out.ID
	}
	last := func(jobs *fakeJobs, id string) galleryop.OpStatus {
		var op galleryop.OpStatus
		Eventually(func() bool {
			h := jobs.history(id)
			if len(h) == 0 {
				return false
			}
			op = h[len(h)-1]
			return op.Processed
		}).Should(BeTrue())
		return op
	}

	It("answers with a job and reports every step, then the result", func() {
		f := &fakeLibrary{steps: allSteps}
		jobs := &fakeJobs{}
		rec := post(f, jobs, `{"sourceId":"pilot","name":"cpu-demo","version":"latest"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		id := jobID(rec)
		done := last(jobs, id)
		Expect(done.Message).To(Equal("completed"))
		Expect(done.Progress).To(BeEquivalentTo(100))
		Expect(done.Error).NotTo(HaveOccurred())
		Expect(f.last).To(Equal([3]string{"pilot", "cpu-demo", "latest"}))
		phases := []string{}
		for _, s := range jobs.history(id) {
			if len(phases) == 0 || phases[len(phases)-1] != s.Phase {
				phases = append(phases, s.Phase)
			}
			Expect(s.GalleryElementName).To(Equal("cpu-demo"))
		}
		Expect(phases).To(Equal(append(append([]string{}, allSteps...), "done")))
		// The fetch step carries the bytes of the image.
		var fetch *galleryop.OpStatus
		for _, s := range jobs.history(id) {
			if s.Phase == backendlib.StepFetching && s.Progress > 0 {
				fetch = &s
			}
		}
		Expect(fetch).NotTo(BeNil())
		Expect(fetch.Progress).To(BeEquivalentTo(50))
		Expect(fetch.TotalFileSize).To(Equal("2 MB"))
	})

	It("keeps the failed step as the phase of the job and gives the reason", func() {
		f := &fakeLibrary{steps: allSteps[:3], err: fmt.Errorf("image too big: %w", downloader.ErrImageTooLarge)}
		jobs := &fakeJobs{}
		id := jobID(post(f, jobs, `{"sourceId":"pilot","name":"cpu-demo","version":"latest"}`))
		done := last(jobs, id)
		Expect(done.Phase).To(Equal(backendlib.StepSize))
		Expect(done.Error).To(MatchError(ContainSubstring(downloader.ErrImageTooLarge.Error())))
		Expect(done.Message).To(HavePrefix("error: "))
	})

	It("words a collision and a missing entry for the administrator", func() {
		for err, want := range map[error]string{
			fmt.Errorf("%w: %q", backendlib.ErrCollision, "cpu-demo"): "already installed from another origin",
			catalog.ErrNotFound: "no such entry",
		} {
			jobs := &fakeJobs{}
			id := jobID(post(&fakeLibrary{err: err}, jobs, `{"sourceId":"p","name":"n","version":"v"}`))
			Expect(last(jobs, id).Message).To(ContainSubstring(want))
		}
	})

	It("refuses a second install of a name that is still running", func() {
		f := &fakeLibrary{block: make(chan struct{})}
		jobs := &fakeJobs{}
		body := `{"sourceId":"pilot","name":"cpu-demo","version":"latest"}`
		e := echo.New()
		handler := localai.InstallBackendFromLibraryEndpoint(f, jobs)
		call := func() *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/backends/library/install", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			Expect(handler(e.NewContext(req, rec))).To(Succeed())
			return rec
		}
		first := call()
		Expect(first.Code).To(Equal(http.StatusOK))
		Expect(call().Code).To(Equal(http.StatusConflict))
		close(f.block)
		last(jobs, jobID(first))
		Eventually(func() int { return call().Code }).Should(Equal(http.StatusOK))
	})

	It("refuses a request that does not name an entry", func() {
		for _, body := range []string{`{}`, `{"sourceId":"pilot","name":"x"}`, `not json`} {
			Expect(post(&fakeLibrary{}, &fakeJobs{}, body).Code).To(Equal(http.StatusBadRequest), body)
		}
	})

	It("lists the updates of the installed library backends", func() {
		f := &fakeLibrary{updates: []backendlib.Update{{Name: "cpu-demo", SourceID: "pilot", InstalledDigest: "sha256:a", Version: "2", Digest: "sha256:b"}}}
		rec := httptest.NewRecorder()
		e := echo.New()
		Expect(localai.BackendLibraryUpdatesEndpoint(f)(e.NewContext(httptest.NewRequest(http.MethodGet, "/backends/library/updates", nil), rec))).To(Succeed())
		var out struct {
			Updates []backendlib.Update `json:"updates"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Updates).To(Equal(f.updates))
	})
})
