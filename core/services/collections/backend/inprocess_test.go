package backend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"
)

const (
	postgresLogChildEnv = "LOCALAI_TEST_POSTGRES_LOG_DSN"
	postgresLogSecret   = "collection-secret-sentinel"
)

// In the child process the spec only starts the vector engine, so the package
// logger writes to the output the parent captures.
func runPostgresLogChild(dsn string) {
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
	kb := newVectorEngine("postgres", client, "", "", "credentials-test", GinkgoT().TempDir(), GinkgoT().TempDir(), "", dsn, 100, 0)
	Expect(kb == nil).To(BeTrue(), "expected initialization to fail without a database")
}

var _ = Describe("PostgreSQL collection logs", func() {
	for _, tc := range []struct{ name, dsn string }{
		{"URL", "postgresql://user:" + postgresLogSecret + "@127.0.0.1:1/test?sslmode=disable&connect_timeout=1"},
		{"keyword DSN", "host=127.0.0.1 port=1 user=user password=" + postgresLogSecret + " dbname=test sslmode=disable connect_timeout=1"},
		{"malformed URL", "postgresql://user:" + postgresLogSecret + "@localhost:invalid/test"},
		{"malformed keyword", "user=user password='" + postgresLogSecret},
	} {
		It("do not expose credentials for a "+tc.name, func() {
			if dsn := os.Getenv(postgresLogChildEnv); dsn != "" {
				runPostgresLogChild(dsn)
				return
			}
			// A subprocess runs this very spec and its output holds the package
			// logs, without changing global logging state.
			focus := regexp.QuoteMeta(CurrentSpecReport().FullText())
			cmd := exec.Command(os.Args[0], "-test.run=^TestCollections$", "-ginkgo.focus="+focus, "-ginkgo.no-color")
			cmd.Env = append(os.Environ(), postgresLogChildEnv+"="+tc.dsn, "LOG_LEVEL=info", "LOG_FORMAT=json")
			output, err := cmd.CombinedOutput()
			Expect(err).ToNot(HaveOccurred(), "log capture failed")
			logs := string(output)
			Expect(logs).To(ContainSubstring("PostgreSQL collection"), "expected collection initialization and failure diagnostics")
			Expect(logs).To(ContainSubstring("Failed to create vector engine collection"), "expected collection initialization and failure diagnostics")
			Expect(logs).To(ContainSubstring("credentials-test"), "expected collection initialization and failure diagnostics")
			Expect(strings.Contains(logs, postgresLogSecret) || strings.Contains(logs, "databaseURL")).To(BeFalse(), "PostgreSQL initialization logs expose connection credentials")
		})
	}
})
