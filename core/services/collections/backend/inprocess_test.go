package backend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestPostgresCollectionLogsDoNotExposeCredentials(t *testing.T) {
	const childEnv = "LOCALAGI_TEST_POSTGRES_LOG_DSN"
	if dsn := os.Getenv(childEnv); dsn != "" {
		// LocalRecall gets a test embedding before it connects to the
		// database, so serve one: the driver error, which can contain
		// the credentials, must still be reached.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0,0],"index":0}],"model":"test","usage":{}}`))
		}))
		defer server.Close()
		config := openai.DefaultConfig("")
		config.BaseURL = server.URL + "/v1"
		client := openai.NewClientWithConfig(config)
		if kb := newVectorEngine("postgres", client, "", "", "credentials-test", t.TempDir(), t.TempDir(), "", dsn, 100, 0); kb != nil {
			t.Fatal("expected initialization to fail without a database")
		}
		return
	}

	const secret = "collection-secret-sentinel"
	for name, dsn := range map[string]string{
		"URL":               "postgresql://user:" + secret + "@127.0.0.1:1/test?sslmode=disable&connect_timeout=1",
		"keyword DSN":       "host=127.0.0.1 port=1 user=user password=" + secret + " dbname=test sslmode=disable connect_timeout=1",
		"malformed URL":     "postgresql://user:" + secret + "@localhost:invalid/test",
		"malformed keyword": "user=user password='" + secret,
	} {
		t.Run(name, func(t *testing.T) {
			// A subprocess captures the package logger without changing global logging state.
			cmd := exec.Command(os.Args[0], "-test.run=^TestPostgresCollectionLogsDoNotExposeCredentials$")
			cmd.Env = append(os.Environ(), childEnv+"="+dsn, "LOG_LEVEL=info", "LOG_FORMAT=json")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("log capture failed: %v", err)
			}
			logs := string(output)
			if !strings.Contains(logs, "PostgreSQL collection") || !strings.Contains(logs, "Failed to create vector engine collection") || !strings.Contains(logs, "credentials-test") {
				t.Fatal("expected collection initialization and failure diagnostics")
			}
			if strings.Contains(logs, secret) || strings.Contains(logs, "databaseURL") {
				t.Fatal("PostgreSQL initialization logs expose connection credentials")
			}
		})
	}
}
