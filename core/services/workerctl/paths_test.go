package workerctl_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/core/services/workerctl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Control paths", func() {
	It("maps every verb onto one path below the prefix, and no two verbs share one", func() {
		seen := map[string]string{}
		for _, verb := range workerctl.AllVerbs() {
			path := workerctl.PathOf(verb)
			Expect(path).To(HavePrefix(workerctl.Prefix), verb)
			Expect(path).ToNot(ContainSubstring("."), verb)
			Expect(seen).ToNot(HaveKey(path), "%s shares a path with %s", verb, seen[path])
			seen[path] = verb
		}
	})

	It("lists each verb once", func() {
		Expect(workerctl.AllVerbs()).To(HaveLen(16))
		Expect(workerctl.AllVerbs()).To(ContainElements(workerctl.VerbModelOp, workerctl.VerbFilesRelease))
		seen := map[string]bool{}
		for _, v := range workerctl.AllVerbs() {
			Expect(seen[v]).To(BeFalse(), v)
			seen[v] = true
		}
	})

	It("keeps the paths of the verbs that older frontends and workers already use", func() {
		Expect(workerctl.PathOf(workerctl.VerbBackendInstall)).To(Equal("/v1/control/backend/install"))
		Expect(workerctl.PathOf(workerctl.VerbModelsRunning)).To(Equal("/v1/control/models/running"))
		Expect(workerctl.PathOf(workerctl.VerbModelOp)).To(Equal("/v1/control/model/op"))
		Expect(workerctl.PathOf(workerctl.VerbFilesListDir)).To(Equal("/v1/control/files/listdir"))
	})
})

var _ = Describe("Control request rules", func() {
	It("refuses anything but a POST", func() {
		rec := httptest.NewRecorder()
		_, ok := workerctl.ReadRequestBody(rec, httptest.NewRequest(http.MethodGet, workerctl.PathOf(workerctl.VerbNodeStop), nil))
		Expect(ok).To(BeFalse())
		Expect(rec.Code).To(Equal(http.StatusMethodNotAllowed))
	})

	It("reads a body up to the bound and refuses a longer one with 400", func() {
		rec := httptest.NewRecorder()
		body, ok := workerctl.ReadRequestBody(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
		Expect(ok).To(BeTrue())
		Expect(string(body)).To(Equal("{}"))

		rec = httptest.NewRecorder()
		big := strings.NewReader(strings.Repeat("x", workerctl.MaxRequestBytes+1))
		_, ok = workerctl.ReadRequestBody(rec, httptest.NewRequest(http.MethodPost, "/", big))
		Expect(ok).To(BeFalse())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("answers an unknown path with 404 and a body that says so, bounded", func() {
		rec := httptest.NewRecorder()
		path := workerctl.Prefix + strings.Repeat("é", 500)
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.URL.Path = path
		workerctl.WriteUnknownPath(rec, req)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
		Expect(rec.Body.String()).To(ContainSubstring("unknown worker control path"))
		Expect(len(rec.Body.String())).To(BeNumerically("<", workerctl.MaxEchoedPathBytes+80))
		Expect(strings.ToValidUTF8(rec.Body.String(), "")).To(Equal(rec.Body.String()), "a cut inside a rune")
	})
})
