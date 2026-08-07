// Package buildproxy provides conservative retrying and traffic telemetry for
// CI build downloads.
package buildproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Event struct {
	Time        time.Time `json:"time"`
	Host        string    `json:"host"`
	Method      string    `json:"method"`
	Path        string    `json:"path,omitempty"`
	Status      int       `json:"status,omitempty"`
	Attempts    int       `json:"attempts"`
	BytesSent   int64     `json:"bytes_sent,omitempty"`
	BytesRead   int64     `json:"bytes_read,omitempty"`
	Intercepted bool      `json:"intercepted,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type SummaryRow struct {
	Host      string `json:"host"`
	Method    string `json:"method"`
	Requests  int64  `json:"requests"`
	Retries   int64  `json:"retries"`
	BytesSent int64  `json:"bytes_sent"`
	BytesRead int64  `json:"bytes_read"`
	Errors    int64  `json:"errors"`
}

type Recorder struct {
	mu   sync.Mutex
	file *os.File
	rows map[string]*SummaryRow
}

func NewRecorder(eventsPath string) (*Recorder, error) {
	if err := os.MkdirAll(filepath.Dir(eventsPath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Recorder{file: f, rows: map[string]*SummaryRow{}}, nil
}

func (r *Recorder) Record(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	_ = json.NewEncoder(r.file).Encode(event)
	key := event.Host + "\x00" + event.Method
	row := r.rows[key]
	if row == nil {
		row = &SummaryRow{Host: event.Host, Method: event.Method}
		r.rows[key] = row
	}
	row.Requests++
	if event.Attempts > 1 {
		row.Retries += int64(event.Attempts - 1)
	}
	row.BytesSent += event.BytesSent
	row.BytesRead += event.BytesRead
	if event.Error != "" || event.Status >= 400 {
		row.Errors++
	}
}

func (r *Recorder) WriteSummary(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := make([]SummaryRow, 0, len(r.rows))
	for _, row := range r.rows {
		rows = append(rows, *row)
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (r *Recorder) Close() error { return r.file.Close() }

type Options struct {
	Transport   http.RoundTripper
	Recorder    *Recorder
	MaxAttempts int
	SpoolDir    string
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func NewHandler(opts Options) func(http.ResponseWriter, *http.Request, string) {
	transport := opts.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if opts.MaxAttempts < 1 {
		opts.MaxAttempts = 3
	}
	if opts.BaseDelay <= 0 {
		opts.BaseDelay = 100 * time.Millisecond
	}
	if opts.MaxDelay <= 0 {
		opts.MaxDelay = 500 * time.Millisecond
	}
	return func(w http.ResponseWriter, request *http.Request, host string) {
		event := Event{Host: hostname(host), Method: request.Method, Path: request.URL.EscapedPath(), Intercepted: true}
		defer func() { opts.Recorder.Record(event) }()
		if request.Body != nil {
			defer func() { _ = request.Body.Close() }()
		}
		event.BytesSent = max(request.ContentLength, 0)
		attempts := 1
		if request.Method == http.MethodGet || request.Method == http.MethodHead {
			attempts = opts.MaxAttempts
		}
		for attempt := 1; attempt <= attempts; attempt++ {
			event.Attempts = attempt
			event.Error = ""
			resp, path, size, err := fetch(request.Context(), transport, request, host, opts.SpoolDir)
			if err == nil && !retryStatus(resp.StatusCode) {
				event.Status, event.BytesRead = resp.StatusCode, size
				copyResponse(w, resp, path, request.Method)
				return
			}
			if resp != nil {
				event.Status = resp.StatusCode
			}
			if path != "" {
				_ = os.Remove(path)
			}
			if err != nil {
				event.Error = err.Error()
			}
			if attempt == attempts {
				break
			}
			if err := sleep(request.Context(), delay(opts.BaseDelay, opts.MaxDelay, attempt)); err != nil {
				event.Error = err.Error()
				break
			}
		}
		http.Error(w, "build proxy: upstream request failed", http.StatusBadGateway)
	}
}

func fetch(ctx context.Context, transport http.RoundTripper, original *http.Request, host, spoolDir string) (*http.Response, string, int64, error) {
	u := *original.URL
	u.Scheme = original.URL.Scheme
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	u.Host = host
	req, err := http.NewRequestWithContext(ctx, original.Method, u.String(), original.Body)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header = cloneHeaders(original.Header)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return nil, "", 0, err
	}
	file, err := os.CreateTemp(spoolDir, "localai-build-proxy-*")
	if err != nil {
		_ = resp.Body.Close()
		return resp, "", 0, err
	}
	path := file.Name()
	size, copyErr := io.Copy(file, resp.Body)
	closeErr := errors.Join(resp.Body.Close(), file.Close())
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr == nil && original.Method != http.MethodHead && resp.ContentLength >= 0 && size != resp.ContentLength {
		copyErr = fmt.Errorf("short response: got %d bytes, expected %d", size, resp.ContentLength)
	}
	return resp, path, size, copyErr
}

func copyResponse(w http.ResponseWriter, resp *http.Response, path, method string) {
	defer func() { _ = os.Remove(path) }()
	for key, values := range resp.Header {
		if hopHeader(key) || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if method == http.MethodHead && resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	} else if info, err := os.Stat(path); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	w.WriteHeader(resp.StatusCode)
	file, err := os.Open(path)
	if err == nil {
		defer func() { _ = file.Close() }()
		_, _ = io.Copy(w, file)
	}
}

func retryStatus(status int) bool {
	switch status {
	case 408, 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func delay(base, limit time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt && d < limit; i++ {
		if d > limit/2 {
			return limit
		}
		d *= 2
	}
	if d > limit {
		return limit
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func hostname(host string) string {
	if u, err := url.Parse("//" + host); err == nil && u.Hostname() != "" {
		return strings.ToLower(u.Hostname())
	}
	return strings.ToLower(host)
}

func cloneHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for key, values := range in {
		if hopHeader(key) || strings.EqualFold(key, "Proxy-Authorization") {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func hopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}
