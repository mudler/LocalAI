package e2e_test

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The streams of the chat, Anthropic and Open Responses endpoints, with and
// without tools, reasoning text, a streamed tool call, and the autoparser's
// ChatDeltas, must stay byte-identical when the streaming path changes for
// speed. The mock backend writes them with its MOCK_BENCH stream. Set
// UPDATE_GOLDEN=1 to record them again.
var _ = Describe("Stream golden output", Label("StreamGolden"), func() {
	uuidRe := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	timeRe := regexp.MustCompile(`"(created|created_at|completed_at)":\d+`)

	const toolSchema = `{"type":"object","properties":{"location":{"type":"string"},"unit":{"type":"string"},"days":{"type":"integer"}},"required":["location"]}`

	endpoints := []struct {
		name string
		path string
		body func(prompt string) string
	}{
		{"chat", "/v1/chat/completions", func(p string) string {
			return fmt.Sprintf(`{"model":"mock-model","stream":true,"messages":[{"role":"user","content":%q}]}`, p)
		}},
		{"chat-tools", "/v1/chat/completions", func(p string) string {
			return fmt.Sprintf(`{"model":"mock-model","stream":true,"messages":[{"role":"user","content":%q}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":%s}}]}`, p, toolSchema)
		}},
		{"anthropic", "/v1/messages", func(p string) string {
			return fmt.Sprintf(`{"model":"mock-model","stream":true,"max_tokens":512,"messages":[{"role":"user","content":%q}]}`, p)
		}},
		{"anthropic-thinking", "/v1/messages", func(p string) string {
			return fmt.Sprintf(`{"model":"mock-model","stream":true,"max_tokens":512,"thinking":{"type":"enabled","budget_tokens":256},"messages":[{"role":"user","content":%q}]}`, p)
		}},
		{"anthropic-tools", "/v1/messages", func(p string) string {
			return fmt.Sprintf(`{"model":"mock-model","stream":true,"max_tokens":512,"messages":[{"role":"user","content":%q}],"tools":[{"name":"get_weather","input_schema":%s}]}`, p, toolSchema)
		}},
		{"responses", "/v1/responses", func(p string) string {
			return fmt.Sprintf(`{"model":"mock-model","stream":true,"input":%q}`, p)
		}},
		{"responses-tools", "/v1/responses", func(p string) string {
			return fmt.Sprintf(`{"model":"mock-model","stream":true,"input":%q,"tools":[{"type":"function","name":"get_weather","parameters":%s}]}`, p, toolSchema)
		}},
	}
	streams := []string{
		"n=4",
		"reasoning=3 n=3",
		"n=3 tool=1",
		"reasoning=2 n=2 tool=1",
		"n=3 deltas=1",
		"reasoning=2 n=3 deltas=1",
		"reasoning=2 n=2 tool=1 deltas=1",
	}

	for _, ep := range endpoints {
		It("keeps the "+ep.name+" streams byte-identical", func() {
			base := strings.TrimSuffix(apiURL, "/v1")
			var got strings.Builder
			for _, stream := range streams {
				resp, err := http.Post(base+ep.path, "application/json", strings.NewReader(ep.body("MOCK_BENCH "+stream)))
				Expect(err).ToNot(HaveOccurred())
				raw, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(http.StatusOK), string(raw))
				raw = uuidRe.ReplaceAll(raw, []byte("UUID"))
				raw = timeRe.ReplaceAll(raw, []byte(`"$1":0`))
				fmt.Fprintf(&got, "=== %s\n%s\n", stream, raw)
			}

			golden := filepath.Join("testdata", "stream_golden", ep.name+".golden")
			if os.Getenv("UPDATE_GOLDEN") != "" {
				Expect(os.MkdirAll(filepath.Dir(golden), 0o755)).To(Succeed())
				Expect(os.WriteFile(golden, []byte(got.String()), 0o644)).To(Succeed())
			}
			want, err := os.ReadFile(golden)
			Expect(err).ToNot(HaveOccurred(), "record it with UPDATE_GOLDEN=1")
			Expect(got.String()).To(Equal(string(want)))
		})
	}
})
