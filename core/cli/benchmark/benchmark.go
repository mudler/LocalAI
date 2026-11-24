// SPDX-License-Identifier: MIT
// Package benchmark measures text inference through a running LocalAI server.
package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

type Command struct {
	Models    []string      `arg:"" required:"" help:"Configured text model names to benchmark sequentially."`
	Endpoint  string        `default:"http://127.0.0.1:8080" help:"LocalAI server URL, optionally ending in /v1."`
	APIKey    string        `name:"api-key" env:"LOCALAI_API_KEY,API_KEY" help:"API key for the server."`
	Prompt    string        `default:"Explain why the sky is blue." help:"User prompt sent with every request."`
	MaxTokens int           `default:"128" help:"Maximum completion tokens per request."`
	Runs      int           `default:"3" help:"Measured requests per model."`
	Warmup    int           `default:"1" help:"Unmeasured requests before each model's measured runs."`
	Timeout   time.Duration `default:"5m" help:"Timeout for each request."`
	JSON      bool          `name:"json" help:"Write settings and raw samples as JSON."`
}

type settings struct {
	Endpoint    string  `json:"endpoint"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens"`
	Runs        int     `json:"runs"`
	Warmup      int     `json:"warmup"`
	Timeout     string  `json:"timeout"`
	Temperature float64 `json:"temperature"`
	Stream      bool    `json:"stream"`
}
type sample struct {
	LatencySeconds   float64 `json:"latency_seconds"`
	PromptTokens     *int    `json:"prompt_tokens"`
	CompletionTokens *int    `json:"completion_tokens"`
}
type modelResult struct {
	Model                     string   `json:"model"`
	Samples                   []sample `json:"samples"`
	MinSeconds                float64  `json:"min_seconds"`
	MeanSeconds               float64  `json:"mean_seconds"`
	MaxSeconds                float64  `json:"max_seconds"`
	CompletionTokensPerSecond *float64 `json:"completion_tokens_per_second"`
}
type report struct {
	Settings settings      `json:"settings"`
	Results  []modelResult `json:"results"`
}

func (c *Command) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return c.run(ctx, os.Stdout)
}

func completionURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") {
		return "", errors.New("endpoint must be an HTTP(S) URL without credentials, query, or fragment")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}
	u.Path = path + "/chat/completions"
	u.RawPath = ""
	return u.String(), nil
}

func (c *Command) run(ctx context.Context, out io.Writer) error {
	endpoint, err := completionURL(c.Endpoint)
	if err != nil {
		return err
	}
	if c.Runs <= 0 || c.Warmup < 0 || c.MaxTokens <= 0 || c.Timeout <= 0 {
		return errors.New("runs, max-tokens, and timeout must be positive; warmup must be nonnegative")
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return errors.New("prompt must not be blank")
	}
	if len(c.Models) == 0 {
		return errors.New("at least one model is required")
	}
	for _, model := range c.Models {
		if strings.TrimSpace(model) == "" {
			return errors.New("model names must not be blank")
		}
	}
	client := httpclient.NewWithTimeout(c.Timeout)
	defer client.CloseIdleConnections()
	result := report{Settings: settings{Endpoint: endpoint, Prompt: c.Prompt, MaxTokens: c.MaxTokens, Runs: c.Runs, Warmup: c.Warmup, Timeout: c.Timeout.String()}}
	for _, model := range c.Models {
		measured := modelResult{Model: model}
		for i := 0; i < c.Warmup; i++ {
			if _, err := c.request(ctx, client, endpoint, model); err != nil {
				return fmt.Errorf("model %q warmup %d: %w", model, i+1, err)
			}
		}
		for i := 0; i < c.Runs; i++ {
			s, err := c.request(ctx, client, endpoint, model)
			if err != nil {
				return fmt.Errorf("model %q run %d: %w", model, i+1, err)
			}
			measured.Samples = append(measured.Samples, s)
		}
		measured.summarize()
		result.Results = append(result.Results, measured)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Buffer the complete report so a failed model never leaves partial results.
	var buffer bytes.Buffer
	if c.JSON {
		encoder := json.NewEncoder(&buffer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return err
		}
	} else {
		table := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(table, "MODEL\tRUNS\tMIN (s)\tMEAN (s)\tMAX (s)\tEND-TO-END TOKENS/s")
		for _, r := range result.Results {
			throughput := "N/A"
			if r.CompletionTokensPerSecond != nil {
				throughput = fmt.Sprintf("%.2f", *r.CompletionTokensPerSecond)
			}
			_, _ = fmt.Fprintf(table, "%s\t%d\t%.4f\t%.4f\t%.4f\t%s\n", r.Model, len(r.Samples), r.MinSeconds, r.MeanSeconds, r.MaxSeconds, throughput)
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}
	_, err = io.Copy(out, &buffer)
	return err
}

func (c *Command) request(ctx context.Context, client *http.Client, endpoint, model string) (sample, error) {
	var s sample
	body, err := json.Marshal(struct {
		Model       string              `json:"model"`
		Messages    []map[string]string `json:"messages"`
		MaxTokens   int                 `json:"max_tokens"`
		Temperature float64             `json:"temperature"`
		Stream      bool                `json:"stream"`
	}{Model: model, Messages: []map[string]string{{"role": "user", "content": c.Prompt}}, MaxTokens: c.MaxTokens})
	if err != nil {
		return s, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return s, errors.New("cannot create benchmark request")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return s, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return s, fmt.Errorf("request timed out: %w", context.DeadlineExceeded)
		}
		if errors.Is(err, httpclient.ErrRedirectBlocked) {
			return s, httpclient.ErrRedirectBlocked
		}
		// Transport errors and server responses can echo credentials.
		return s, errors.New("HTTP request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	var response struct {
		Choices []json.RawMessage `json:"choices"`
		Usage   struct {
			PromptTokens     *int `json:"prompt_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
		} `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&response); err != nil {
		if ctx.Err() != nil {
			return s, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return s, fmt.Errorf("request timed out: %w", context.DeadlineExceeded)
		}
		return s, errors.New("invalid JSON response")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return s, errors.New("invalid trailing response data")
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		var detail struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(response.Error, &detail) == nil && detail.Message != "" {
			message := detail.Message
			if c.APIKey != "" {
				message = strings.ReplaceAll(message, c.APIKey, "[redacted]")
			}
			return s, fmt.Errorf("server returned an API error: %s", message)
		}
		return s, errors.New("server returned an API error")
	}
	if len(response.Choices) == 0 {
		return s, errors.New("response contains no choices")
	}
	s.LatencySeconds = time.Since(start).Seconds()
	s.PromptTokens = response.Usage.PromptTokens
	s.CompletionTokens = response.Usage.CompletionTokens
	if (s.PromptTokens != nil && *s.PromptTokens < 0) || (s.CompletionTokens != nil && *s.CompletionTokens < 0) {
		return s, errors.New("response contains negative token counts")
	}
	return s, nil
}

func (r *modelResult) summarize() {
	r.MinSeconds = r.Samples[0].LatencySeconds
	var seconds, tokens float64
	available := true
	for _, s := range r.Samples {
		seconds += s.LatencySeconds
		r.MinSeconds = min(r.MinSeconds, s.LatencySeconds)
		r.MaxSeconds = max(r.MaxSeconds, s.LatencySeconds)
		if s.CompletionTokens == nil {
			available = false
		} else {
			tokens += float64(*s.CompletionTokens)
		}
	}
	r.MeanSeconds = seconds / float64(len(r.Samples))
	if available && seconds > 0 {
		rate := tokens / seconds
		r.CompletionTokensPerSecond = &rate
	}
}
