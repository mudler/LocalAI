package nodes

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestServer(t *testing.T, token string, maxUploadSize int64) (*httptest.Server, string, string, string) {
	t.Helper()

	stagingDir := t.TempDir()
	modelsDir := t.TempDir()
	dataDir := t.TempDir()

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBearerToken(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/v1/files/")
		switch r.Method {
		case http.MethodPut:
			handleUpload(w, r, stagingDir, modelsDir, dataDir, key, maxUploadSize)
		case http.MethodGet:
			handleDownload(w, r, stagingDir, modelsDir, dataDir, key)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return ts, stagingDir, modelsDir, dataDir
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	ts, _, _, _ := setupTestServer(t, "secret-token", 0)

	content := "hello distributed world"

	// Upload
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/myfile.txt", strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d", resp.StatusCode)
	}

	// Download
	req, err = http.NewRequest(http.MethodGet, ts.URL+"/v1/files/myfile.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != content {
		t.Fatalf("round-trip content mismatch: got %q, want %q", string(body), content)
	}
}

func TestUploadMaxBytesReader(t *testing.T) {
	ts, _, _, _ := setupTestServer(t, "tok", 100)

	bigPayload := bytes.Repeat([]byte("x"), 200)
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/big.bin", bytes.NewReader(bigPayload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// The server should reject the oversized upload with an error status
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected non-200 status for oversized upload, got 200")
	}
}

func TestPathTraversalPrevention(t *testing.T) {
	ts, stagingDir, _, _ := setupTestServer(t, "tok", 0)

	// Attempt to upload with a path traversal key
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/../../../etc/passwd", strings.NewReader("evil"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected non-200 status for path traversal attempt, got 200")
	}

	// Also verify the file was not created outside the staging directory
	badPath := filepath.Join(stagingDir, "..", "..", "..", "etc", "passwd")
	if _, err := os.Stat(badPath); err == nil {
		t.Fatal("path traversal: file was written outside staging directory")
	}
}

func TestResolveKeyToDir(t *testing.T) {
	stagingDir := "/tmp/staging"
	modelsDir := "/tmp/models"
	dataDir := "/tmp/data"

	tests := []struct {
		key         string
		wantDir     string
		wantRelName string
	}{
		{"models/foo", modelsDir, "foo"},
		{"models/subdir/bar.bin", modelsDir, "subdir/bar.bin"},
		{"data/bar", dataDir, "bar"},
		{"data/nested/file.txt", dataDir, "nested/file.txt"},
		{"other", stagingDir, "other"},
		{"random/path/file.gguf", stagingDir, "random/path/file.gguf"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			gotDir, gotRel := resolveKeyToDir(tt.key, stagingDir, modelsDir, dataDir)
			if gotDir != tt.wantDir {
				t.Errorf("resolveKeyToDir(%q) dir = %q, want %q", tt.key, gotDir, tt.wantDir)
			}
			if gotRel != tt.wantRelName {
				t.Errorf("resolveKeyToDir(%q) relName = %q, want %q", tt.key, gotRel, tt.wantRelName)
			}
		})
	}
}

func TestBearerTokenAuth(t *testing.T) {
	ts, _, _, _ := setupTestServer(t, "correct-token", 0)

	// Pre-upload a file so GET can succeed with the right token
	content := "auth test content"
	putReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/auth-test.txt", strings.NewReader(content))
	putReq.Header.Set("Authorization", "Bearer correct-token")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("pre-upload failed: %d", putResp.StatusCode)
	}

	// No token -> 401
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/files/auth-test.txt", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: expected 401, got %d", resp.StatusCode)
	}

	// Wrong token -> 401
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/v1/files/auth-test.txt", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: expected 401, got %d", resp.StatusCode)
	}

	// Correct token -> success
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/v1/files/auth-test.txt", nil)
	req.Header.Set("Authorization", "Bearer correct-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct token: expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != content {
		t.Fatalf("content mismatch: got %q, want %q", string(body), content)
	}
}
