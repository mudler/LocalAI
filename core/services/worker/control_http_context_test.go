package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The NATS server hands a handler a context that nothing cancels. A verb such as
// an unload bounds its own work, and it must run to the end when the caller goes
// away, so that the HTTP server behaves in the same way.
var _ = Describe("The context of a verb over HTTP", func() {
	// callAndLeave sends a request and cancels it as soon as the handler runs.
	callAndLeave := func(srv *httptest.Server, path string, started <-chan struct{}) {
		GinkgoHelper()
		reqCtx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, srv.URL+path, strings.NewReader("{}"))
		Expect(err).ToNot(HaveOccurred())
		go func() {
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		Eventually(started).Should(BeClosed())
		cancel()
		// Long enough for the server to see the connection close.
		time.Sleep(200 * time.Millisecond)
	}

	It("is not cancelled when the caller goes away, for a verb with one reply", func() {
		ctl := newHTTPControlServer()
		started, finish := make(chan struct{}), make(chan struct{})
		seen := make(chan error, 1)
		Expect(ctl.handle(verbModelUnload, func(ctx context.Context, _ []byte) (any, error) {
			close(started)
			<-finish
			seen <- ctx.Err()
			return workerctl.ModelUnloadReply{Success: true}, nil
		})).To(Succeed())
		srv := httptest.NewServer(ctl)
		DeferCleanup(srv.Close)

		callAndLeave(srv, workerctl.PathOf(workerctl.VerbModelUnload), started)
		close(finish)
		var got error
		Eventually(seen).Should(Receive(&got))
		Expect(got).ToNot(HaveOccurred(), "the free of a backend must not be abandoned half way")
	})

	It("is not cancelled when the caller goes away, for a verb that reports progress", func() {
		ctl := newHTTPControlServer()
		started, finish := make(chan struct{}), make(chan struct{})
		seen := make(chan error, 1)
		Expect(ctl.handleWithProgress(verbBackendInstall, func(ctx context.Context, _ []byte, _ progressSink) (any, error) {
			close(started)
			<-finish
			seen <- ctx.Err()
			return workerctl.BackendInstallReply{Success: true}, nil
		})).To(Succeed())
		srv := httptest.NewServer(ctl)
		DeferCleanup(srv.Close)

		callAndLeave(srv, workerctl.PathOf(workerctl.VerbBackendInstall), started)
		close(finish)
		var got error
		Eventually(seen).Should(Receive(&got))
		Expect(got).ToNot(HaveOccurred())
	})
})
