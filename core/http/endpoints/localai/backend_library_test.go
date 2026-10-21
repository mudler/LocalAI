package localai_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeLibrary struct {
	err  error
	last [3]string
}

func (f *fakeLibrary) Install(_ context.Context, source, backend, version string, _ func(string, string, string, float64)) error {
	f.last = [3]string{source, backend, version}
	return f.err
}

var _ = Describe("install a backend from a library", func() {
	post := func(f *fakeLibrary, body string) *httptest.ResponseRecorder {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/backends/library/install", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		Expect(localai.InstallBackendFromLibraryEndpoint(f)(e.NewContext(req, rec))).To(Succeed())
		return rec
	}

	It("installs the named entry", func() {
		f := &fakeLibrary{}
		rec := post(f, `{"sourceId":"pilot","name":"cpu-demo","version":"latest"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(f.last).To(Equal([3]string{"pilot", "cpu-demo", "latest"}))
	})

	It("refuses a request that does not name an entry", func() {
		for _, body := range []string{`{}`, `{"sourceId":"pilot","name":"x"}`, `not json`} {
			Expect(post(&fakeLibrary{}, body).Code).To(Equal(http.StatusBadRequest), body)
		}
	})

	DescribeTable("maps what the install says to a status",
		func(err error, status int) {
			Expect(post(&fakeLibrary{err: err}, `{"sourceId":"p","name":"n","version":"v"}`).Code).To(Equal(status))
		},
		Entry("no such entry", catalog.ErrNotFound, http.StatusNotFound),
		Entry("collision", fmt.Errorf("%w: %q", backendlib.ErrCollision, "n"), http.StatusConflict),
		Entry("strict integrity", backendlib.ErrIntegrity, http.StatusBadRequest),
		Entry("library unavailable", catalog.ErrUnavailable, http.StatusServiceUnavailable),
		Entry("too large", fmt.Errorf("failed: %w", downloader.ErrImageTooLarge), http.StatusRequestEntityTooLarge),
		Entry("anything else", fmt.Errorf("boom"), http.StatusInternalServerError),
	)
})
