// Command streamload measures the CPU time and the allocations that a LocalAI
// server spends on streamed requests. Run the server with a model served by
// the mock backend (tests/e2e/mock-backend), which streams MOCK_BENCH replies
// as fast as the client reads them: what the server spends is then its own
// overhead, not the model's.
//
// The CPU time comes from /proc/<pid>/stat, so the tool runs on Linux, on the
// host of the server. The allocations come from the go_memstats counters of
// the server's /metrics endpoint.
//
//	go run ./scripts/bench/cmd/streamload -pid $(pgrep -x local-ai) \
//	  -model bench -endpoint chat -c 16 -r 8 -spec "n=500" -tokens 500
//
// It prints one JSON line with the medians and percentiles of the run.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

const toolSchema = `{"type":"object","properties":{"location":{"type":"string"},"unit":{"type":"string"},"days":{"type":"integer"}},"required":["location"]}`

// body returns the path and the request body of a streamed request.
func body(endpoint, model, prompt string) (string, string, error) {
	switch endpoint {
	case "chat":
		return "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":%q}]}`, model, prompt), nil
	case "chat-tools":
		return "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":%q}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":%s}}]}`, model, prompt, toolSchema), nil
	case "anthropic":
		return "/v1/messages", fmt.Sprintf(`{"model":%q,"stream":true,"max_tokens":4096,"messages":[{"role":"user","content":%q}]}`, model, prompt), nil
	case "anthropic-tools":
		return "/v1/messages", fmt.Sprintf(`{"model":%q,"stream":true,"max_tokens":4096,"messages":[{"role":"user","content":%q}],"tools":[{"name":"get_weather","input_schema":%s}]}`, model, prompt, toolSchema), nil
	case "responses":
		return "/v1/responses", fmt.Sprintf(`{"model":%q,"stream":true,"input":%q}`, model, prompt), nil
	case "responses-tools":
		return "/v1/responses", fmt.Sprintf(`{"model":%q,"stream":true,"input":%q,"tools":[{"type":"function","name":"get_weather","parameters":%s}]}`, model, prompt, toolSchema), nil
	}
	return "", "", fmt.Errorf("unknown endpoint %q", endpoint)
}

// cpuSeconds returns the user and system CPU time of a process.
func cpuSeconds(pid int) (float64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	s := string(b)
	fields := strings.Fields(s[strings.LastIndex(s, ")")+2:])
	user, _ := strconv.ParseFloat(fields[11], 64)
	system, _ := strconv.ParseFloat(fields[12], 64)
	return (user + system) / 100, nil // clock ticks of 10 ms
}

var (
	mallocsRe = regexp.MustCompile(`(?m)^go_memstats_mallocs_total (\S+)`)
	bytesRe   = regexp.MustCompile(`(?m)^go_memstats_alloc_bytes_total (\S+)`)
)

// allocations returns the allocation counters of the server.
func allocations(client *http.Client, base string) (count, size float64, err error) {
	req, err := http.NewRequest(http.MethodGet, base+"/metrics", nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}
	m, s := mallocsRe.FindSubmatch(b), bytesRe.FindSubmatch(b)
	if m == nil || s == nil {
		return 0, 0, fmt.Errorf("no go_memstats counters in /metrics")
	}
	count, _ = strconv.ParseFloat(string(m[1]), 64)
	size, _ = strconv.ParseFloat(string(s[1]), 64)
	return count, size, nil
}

func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	return v[int(float64(len(v)-1)*p)]
}

type result struct {
	ttfb   float64
	gaps   []float64
	chunks int
}

func stream(client *http.Client, url, payload string) (result, error) {
	var r result
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return r, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return r, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return r, fmt.Errorf("status %d: %s", resp.StatusCode, b)
	}
	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	var last time.Time
	for {
		line, err := reader.ReadSlice('\n')
		if bytes.HasPrefix(line, []byte("data:")) {
			now := time.Now()
			if last.IsZero() {
				r.ttfb = now.Sub(start).Seconds()
			} else {
				r.gaps = append(r.gaps, now.Sub(last).Seconds())
			}
			last = now
			r.chunks++
		}
		if err != nil {
			return r, nil
		}
	}
}

func main() {
	base := flag.String("url", "http://127.0.0.1:8080", "base URL of the server")
	pid := flag.Int("pid", 0, "process ID of the server, for its CPU time")
	model := flag.String("model", "bench", "model name")
	endpoint := flag.String("endpoint", "chat", "chat, chat-tools, anthropic, anthropic-tools, responses or responses-tools")
	conc := flag.Int("c", 1, "concurrent streams")
	reqs := flag.Int("r", 4, "requests per stream")
	spec := flag.String("spec", "n=500", "fields of the MOCK_BENCH marker")
	tokens := flag.Int("tokens", 500, "tokens the backend sends for each request")
	flag.Parse()

	path, payload, err := body(*endpoint, *model, "MOCK_BENCH "+*spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	client := httpclient.New(httpclient.WithTransport(func() http.RoundTripper {
		t := httpclient.HardenedTransport()
		t.MaxIdleConnsPerHost = 256
		t.DisableCompression = true
		return t
	}()))
	url := *base + path

	// The first request loads the model.
	if _, err := stream(client, url, payload); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	allocs0, bytes0, err := allocations(client, *base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cpu0, err := cpuSeconds(*pid)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	t0 := time.Now()
	var (
		mu     sync.Mutex
		ttfbs  []float64
		gaps   []float64
		chunks int
		errs   int
		wg     sync.WaitGroup
	)
	for range *conc {
		wg.Go(func() {
			for range *reqs {
				r, err := stream(client, url, payload)
				mu.Lock()
				if err != nil {
					errs++
					fmt.Fprintln(os.Stderr, err)
				} else {
					ttfbs = append(ttfbs, r.ttfb)
					gaps = append(gaps, r.gaps...)
					chunks += r.chunks
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	wall := time.Since(t0).Seconds()
	cpu1, _ := cpuSeconds(*pid)
	allocs1, bytes1, _ := allocations(client, *base)

	requests := float64(*conc * *reqs)
	total := requests * float64(*tokens)
	out, _ := json.Marshal(map[string]any{
		"endpoint": *endpoint, "model": *model, "spec": *spec, "streams": *conc, "requests": requests,
		"cpu_us_per_token":     (cpu1 - cpu0) / total * 1e6,
		"allocs_per_token":     (allocs1 - allocs0) / total,
		"bytes_per_token":      (bytes1 - bytes0) / total,
		"tokens_per_second":    total / wall,
		"ttfb_p50_ms":          percentile(ttfbs, 0.5) * 1e3,
		"ttfb_p99_ms":          percentile(ttfbs, 0.99) * 1e3,
		"chunk_gap_p99_us":     percentile(gaps, 0.99) * 1e6,
		"chunks_per_request":   float64(chunks) / requests,
		"errors":               errs,
	})
	fmt.Println(string(out))
}
