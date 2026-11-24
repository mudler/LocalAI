package localai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("serving the file of a collection entry", func() {
	write := func(body string) string {
		p := filepath.Join(GinkgoT().TempDir(), "guide.txt")
		Expect(os.WriteFile(p, []byte(body), 0o600)).To(Succeed())
		return p
	}
	serve := func(ctx context.Context, p string, header http.Header) (*httptest.ResponseRecorder, error) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		for k, v := range header {
			req.Header[k] = v
		}
		rec := httptest.NewRecorder()
		err := serveFileUntil(ctx, e.NewContext(req, rec), p)
		return rec, err
	}

	It("serves the whole file with its type and honours a range", func() {
		p := write("how to reset the router")
		rec, err := serve(context.Background(), p, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal("how to reset the router"))
		Expect(rec.Header().Get("Content-Type")).To(HavePrefix("text/plain"))
		rec, err = serve(context.Background(), p, http.Header{"Range": {"bytes=0-2"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Code).To(Equal(http.StatusPartialContent))
		Expect(rec.Body.String()).To(Equal("how"))
	})

	It("sends no byte of the file once its context ended", func() {
		p := write(strings.Repeat("secret ", 1024))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec, err := serve(ctx, p, nil)
		Expect(err).To(MatchError(context.Canceled))
		Expect(rec.Body.String()).NotTo(ContainSubstring("secret"))
	})

	It("refuses a directory", func() {
		_, err := serve(context.Background(), GinkgoT().TempDir(), nil)
		Expect(err).To(HaveOccurred())
	})
})
