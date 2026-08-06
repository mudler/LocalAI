package downloader_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/pkg/downloader"
)

// ReadWithCallback used to hand the body of an error response to the callback
// with a nil error, so a 404 page was indistinguishable from an empty gallery
// index and callers had no way to notice the source was down.
func TestReadWithCallbackFailsOnErrorStatus(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusBadGateway} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", status)
		}))

		called := false
		err := downloader.URI(srv.URL).ReadWithCallback(t.TempDir(), func(string, []byte) error {
			called = true
			return nil
		})
		srv.Close()

		if err == nil {
			t.Errorf("status %d: want an error", status)
		} else if !strings.Contains(err.Error(), "status code") {
			t.Errorf("status %d: error %q does not mention the status code", status, err)
		}
		if called {
			t.Errorf("status %d: the error body was passed to the callback as content", status)
		}
	}
}

func TestReadWithCallbackSucceedsOnOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("- name: a\n"))
	}))
	defer srv.Close()

	var got string
	if err := downloader.URI(srv.URL).ReadWithAuthorizationAndCallback(context.Background(), t.TempDir(), "",
		func(_ string, d []byte) error {
			got = string(d)
			return nil
		}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "- name: a\n" {
		t.Errorf("body = %q", got)
	}
}
