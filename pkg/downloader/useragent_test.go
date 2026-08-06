package downloader_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/downloader"
)

// stampVersion pins a recognisable build version for the duration of a test so
// the expected User-Agent is not the empty-version ("source build") form, which
// would still match if the version were dropped from the header.
func stampVersion(t *testing.T) {
	t.Helper()
	saved := internal.Version
	internal.Version = "v9.9.9"
	t.Cleanup(func() { internal.Version = saved })
}

// assertUserAgent fails when the header is not exactly what internal.UserAgent
// produces, and separately when it does not name the build version — the second
// check is what catches a header that is set but carries the wrong identity.
func assertUserAgent(t *testing.T, site, got string) {
	t.Helper()
	if got != internal.UserAgent() {
		t.Errorf("%s: User-Agent = %q, want %q", site, got, internal.UserAgent())
	}
	if !strings.Contains(got, "LocalAI/v9.9.9") {
		t.Errorf("%s: User-Agent = %q, want it to name the build version", site, got)
	}
}

// The gallery index is fetched through this package. Without a User-Agent the
// request is indistinguishable from any other Go program, which is both
// unhelpful to the hosts serving us and inconsistent with pkg/oci, which has
// always identified itself.
func TestReadWithCallbackSendsUserAgent(t *testing.T) {
	stampVersion(t)

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("- name: a\n"))
	}))
	defer srv.Close()

	uri := downloader.URI(srv.URL)
	if err := uri.ReadWithCallback(t.TempDir(), func(string, []byte) error { return nil }); err != nil {
		t.Fatalf("ReadWithCallback: %v", err)
	}

	assertUserAgent(t, "gallery read", got)
}

// Model files are the bulk of what LocalAI pulls; they go through
// newDownloadRequest, which every download and every resume probe shares.
func TestDownloadFileSendsUserAgent(t *testing.T) {
	stampVersion(t)

	seen := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("User-Agent")
		w.Header().Set("Accept-Ranges", "bytes")
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()

	uri := downloader.URI(srv.URL + "/file.bin")
	target := filepath.Join(t.TempDir(), "file.bin")
	if err := uri.DownloadFile(target, "", 1, 1, func(string, string, string, float64) {}); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}

	close(seen)
	n := 0
	for ua := range seen {
		n++
		assertUserAgent(t, "download", ua)
	}
	if n == 0 {
		t.Fatal("server saw no requests")
	}
}

// ContentLength builds its own HEAD request rather than going through
// newDownloadRequest, so it needs its own coverage.
func TestContentLengthHeadSendsUserAgent(t *testing.T) {
	stampVersion(t)

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Header().Set("Content-Length", "7")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	size, err := downloader.URI(srv.URL + "/file.bin").ContentLength(context.Background())
	if err != nil {
		t.Fatalf("ContentLength: %v", err)
	}
	if size != 7 {
		t.Fatalf("ContentLength = %d, want 7", size)
	}

	assertUserAgent(t, "content-length HEAD", got)
}

// When the HEAD carries no Content-Length, ContentLength falls back to a
// one-byte Range GET built at a third, separate site.
func TestContentLengthRangeFallbackSendsUserAgent(t *testing.T) {
	stampVersion(t)

	var rangeUA string
	var sawRange bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			// No Content-Length: this is what pushes ContentLength onto the
			// Range fallback path.
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusOK)
			return
		}
		sawRange = true
		rangeUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Range", "bytes 0-0/4242")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	size, err := downloader.URI(srv.URL + "/file.bin").ContentLength(context.Background())
	if err != nil {
		t.Fatalf("ContentLength: %v", err)
	}
	if size != 4242 {
		t.Fatalf("ContentLength = %d, want 4242", size)
	}
	if !sawRange {
		t.Fatal("server never saw the Range GET; the fallback path was not exercised")
	}

	assertUserAgent(t, "content-length Range GET", rangeUA)
}

// The HuggingFace safety scan is the one outbound request in this package that
// does not live in uri.go, and it was the easiest one to overlook.
func TestHuggingFaceScanSendsUserAgent(t *testing.T) {
	stampVersion(t)

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"repositoryId":"owner/repo","scansDone":true}`))
	}))
	defer srv.Close()

	savedEndpoint := downloader.HF_ENDPOINT
	downloader.HF_ENDPOINT = srv.URL
	t.Cleanup(func() { downloader.HF_ENDPOINT = savedEndpoint })

	uri := downloader.URI(srv.URL + "/owner/repo/resolve/main/model.gguf")
	if _, err := downloader.HuggingFaceScan(uri); err != nil {
		t.Fatalf("HuggingFaceScan: %v", err)
	}

	assertUserAgent(t, "huggingface scan", got)
}
