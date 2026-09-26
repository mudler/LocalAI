package main

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// recordedRequest is what the fake upstream saw for one call. JSON bodies land
// in JSON; multipart bodies land in Fields and Files (field name to content).
type recordedRequest struct {
	Method string
	Path   string
	Auth   string
	JSON   map[string]any
	Fields map[string]string
	Files  map[string]string
}

// scriptedResponse is the reply for one path. SSE, when set, is written as
// "data: <frame>" events and wins over Body.
type scriptedResponse struct {
	Status      int
	ContentType string
	Body        string
	SSE         []string
}

// fakeUpstream stands in for a remote LocalAI: it records every request and
// answers each path with the response scripted for it (404 otherwise).
type fakeUpstream struct {
	*httptest.Server

	mu        sync.Mutex
	requests  []recordedRequest
	responses map[string]scriptedResponse
}

func newFakeUpstream() *fakeUpstream {
	f := &fakeUpstream{responses: map[string]scriptedResponse{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// newFakeUpstreamWithHandler serves every request with h instead of the
// scripted responses, for tests that need to control timing.
func newFakeUpstreamWithHandler(h http.HandlerFunc) *fakeUpstream {
	return &fakeUpstream{Server: httptest.NewServer(h), responses: map[string]scriptedResponse{}}
}

func (f *fakeUpstream) script(path string, r scriptedResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[path] = r
}

// replyJSON scripts a 200 JSON response for path.
func (f *fakeUpstream) replyJSON(path string, body any) {
	raw, err := json.Marshal(body)
	Expect(err).NotTo(HaveOccurred())
	f.script(path, scriptedResponse{Status: http.StatusOK, ContentType: "application/json", Body: string(raw)})
}

func (f *fakeUpstream) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

// last returns the single most recent request, failing when none arrived.
func (f *fakeUpstream) last() recordedRequest {
	reqs := f.recorded()
	ExpectWithOffset(1, reqs).NotTo(BeEmpty(), "upstream received no request")
	return reqs[len(reqs)-1]
}

func (f *fakeUpstream) serve(w http.ResponseWriter, r *http.Request) {
	rec := recordedRequest{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")}
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case mediaType == "multipart/form-data":
		rec.Fields, rec.Files = map[string]string{}, map[string]string{}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			if part.FileName() != "" {
				rec.Files[part.FormName()] = string(data)
			} else {
				rec.Fields[part.FormName()] = string(data)
			}
		}
	default:
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &rec.JSON)
		}
	}

	f.mu.Lock()
	f.requests = append(f.requests, rec)
	resp, ok := f.responses[r.URL.Path]
	f.mu.Unlock()

	if !ok {
		http.Error(w, "no scripted response for "+r.URL.Path, http.StatusNotFound)
		return
	}
	if resp.SSE != nil {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, frame := range resp.SSE {
			_, _ = io.WriteString(w, "data: "+frame+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		return
	}
	if resp.ContentType != "" {
		w.Header().Set("Content-Type", resp.ContentType)
	}
	w.WriteHeader(resp.Status)
	_, _ = io.WriteString(w, resp.Body)
}

// loadProxy returns a proxy loaded against the fake upstream with the given
// proxy options merged over sane defaults.
func loadProxy(f *fakeUpstream, mutate func(*pb.ModelOptions)) *LocalAIProxy {
	opts := &pb.ModelOptions{
		Model: "local-name",
		Proxy: &pb.ProxyOptions{UpstreamUrl: f.URL + "/", UpstreamModel: "remote-model"},
	}
	if mutate != nil {
		mutate(opts)
	}
	p := NewLocalAIProxy()
	ExpectWithOffset(1, p.Load(opts)).To(Succeed())
	return p
}

// sseJSON marshals v for use as one SSE frame.
func sseJSON(v any) string {
	raw, err := json.Marshal(v)
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(string(raw))
}
