package llmman

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndpointDefaultsToLlmmanServeDefault(t *testing.T) {
	t.Setenv(HostEnv, "")
	if got := Endpoint(); got != "http://127.0.0.1:17434" {
		t.Fatalf("got %q", got)
	}
}

func TestEndpointParsesHostForms(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4:9999":                "http://1.2.3.4:9999",
		"1.2.3.4":                     "http://1.2.3.4:17434",
		"http://1.2.3.4:9999":         "http://1.2.3.4:9999",
		"http://1.2.3.4:9999/ignored": "http://1.2.3.4:9999",
		`"1.2.3.4:9999"`:              "http://1.2.3.4:9999",
		// A wildcard bind is meaningful to the server but not to a client,
		// which cannot connect to "every interface".
		"0.0.0.0:9999": "http://127.0.0.1:9999",
		"[::]:9999":    "http://[::1]:9999",
	}
	for in, want := range cases {
		t.Setenv(HostEnv, in)
		if got := Endpoint(); got != want {
			t.Errorf("Endpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckDaemonAcceptsALlmmanDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "0.1.0", "pid": 1})
	}))
	defer srv.Close()

	if err := CheckDaemon(context.Background(), srv.Client(), srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDaemonRejectsANonLlmmanServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()

	err := CheckDaemon(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "not an llmman daemon") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckDaemonReportsNothingListening(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listening now

	err := CheckDaemon(context.Background(), ProbeClient(), url)
	if err == nil || !strings.Contains(err.Error(), "llmman serve") {
		t.Fatalf("expected an actionable error, got %v", err)
	}
}

func ndjson(lines ...map[string]any) string {
	var b strings.Builder
	for _, l := range lines {
		raw, _ := json.Marshal(l)
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func pullServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["model"] == "" {
			t.Error("expected a model field in the pull request")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestPullSucceedsAndForwardsProgress(t *testing.T) {
	srv := pullServer(t, ndjson(
		map[string]any{"status": "pulling manifest"},
		map[string]any{"status": "pulling blobs", "completed": 50, "total": 100},
		map[string]any{"status": "success"},
	), http.StatusOK)
	defer srv.Close()

	var seen []string
	var lastCompleted, lastTotal int64
	err := Pull(context.Background(), srv.Client(), srv.URL, "ghcr.io/org/model:tag",
		func(status string, completed, total int64) {
			seen = append(seen, status)
			lastCompleted, lastTotal = completed, total
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "pulling manifest" {
		t.Fatalf("progress = %v", seen)
	}
	if lastCompleted != 50 || lastTotal != 100 {
		t.Fatalf("byte progress = %d/%d", lastCompleted, lastTotal)
	}
}

func TestPullReportsInBandErrorAtHTTP200(t *testing.T) {
	// The daemon streams errors in-band, so a 200 does not mean success.
	srv := pullServer(t, ndjson(
		map[string]any{"status": "pulling manifest"},
		map[string]any{"error": "unauthorized"},
	), http.StatusOK)
	defer srv.Close()

	err := Pull(context.Background(), srv.Client(), srv.URL, "ref", nil)
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("got %v", err)
	}
}

func TestPullRejectsAStreamThatEndsWithoutSuccess(t *testing.T) {
	srv := pullServer(t, ndjson(map[string]any{"status": "pulling blobs"}), http.StatusOK)
	defer srv.Close()

	err := Pull(context.Background(), srv.Client(), srv.URL, "ref", nil)
	if err == nil || !strings.Contains(err.Error(), "without reporting success") {
		t.Fatalf("got %v", err)
	}
}

func TestPullReportsNonOKStatus(t *testing.T) {
	srv := pullServer(t, `{"error":"bad request"}`, http.StatusBadRequest)
	defer srv.Close()

	if err := Pull(context.Background(), srv.Client(), srv.URL, "ref", nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestPullToleratesANonJSONDiagnosticLine(t *testing.T) {
	body := "not json\n" + ndjson(map[string]any{"status": "success"})
	srv := pullServer(t, body, http.StatusOK)
	defer srv.Close()

	if err := Pull(context.Background(), srv.Client(), srv.URL, "ref", nil); err != nil {
		t.Fatal(err)
	}
}

func TestParseResolveOutput(t *testing.T) {
	dir := t.TempDir()

	got, err := parseResolveOutput(`{"reference":"r","path":"`+dir+`","format":"safetensors"}`, "r")
	if err != nil || got != dir {
		t.Fatalf("got %q, %v", got, err)
	}

	// A diagnostic leaking onto stdout must not break resolution.
	got, err = parseResolveOutput("pulling...\n{\"path\":\""+dir+"\"}\n", "r")
	if err != nil || got != dir {
		t.Fatalf("got %q, %v", got, err)
	}

	// Unknown fields are ignored so the contract can grow.
	if _, err := parseResolveOutput(`{"path":"`+dir+`","mmproj":"/x","future":1}`, "r"); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{
		"",
		"   \n\n",
		"not json",
		`{"no_path":1}`,
		`{"path":""}`,
		`{"path":"/nonexistent/xyzzy"}`,
	} {
		if _, err := parseResolveOutput(bad, "r"); err == nil {
			t.Errorf("expected an error for %q", bad)
		}
	}
}

func TestBinaryHonoursTheOverride(t *testing.T) {
	t.Setenv(BinEnv, "")
	if Binary() != "llmman" {
		t.Fatalf("got %q", Binary())
	}
	t.Setenv(BinEnv, "/opt/bin/llmman")
	if Binary() != "/opt/bin/llmman" {
		t.Fatalf("got %q", Binary())
	}
	// An empty override is a mistake, not a request to run the empty string.
	t.Setenv(BinEnv, "   ")
	if Binary() != "llmman" {
		t.Fatalf("got %q", Binary())
	}
}

func TestResolveReportsAMissingBinary(t *testing.T) {
	t.Setenv(BinEnv, filepath.Join(t.TempDir(), "definitely-not-here"))
	_, err := Resolve(context.Background(), "ref")
	if err == nil || !strings.Contains(err.Error(), "install llmman") {
		t.Fatalf("expected an actionable error, got %v", err)
	}
}
