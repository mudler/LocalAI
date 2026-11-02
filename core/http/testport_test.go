package http_test

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
)

// startTestHTTPServer owns its listener for the lifetime of the spec. Binding
// loopback port zero avoids both collisions with unrelated services and the
// race introduced by probing a free port before starting the server.
func startTestHTTPServer(handler http.Handler) string {
	server := httptest.NewServer(handler)
	DeferCleanup(server.Close)
	return server.URL
}
