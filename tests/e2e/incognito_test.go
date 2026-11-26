package e2e_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/privacy"
)

// The proof of incognito mode. A chat (and a chat with a saved agent) sends a
// unique sentinel to the model; afterwards every place the server could keep
// it is scanned: the files under the data path (databases, traces, the
// workspace, the collections), the generated-content directory, the model
// directory (prompt caches), the traces in memory and the log the server
// wrote meanwhile. The same scan finds the sentinel of a normal chat, so a
// scan that looks in the wrong place cannot pass by accident.
//
// This test guards the stores added later: a new place that keeps content
// must ask privacy.Allows, or the zero-retention case fails here.
var _ = Describe("Incognito chats keep nothing", Label("Chats", "Incognito"), func() {
	var logs *logCapture

	BeforeEach(func() {
		cfg := localAIApp.ApplicationConfig()
		was := cfg.EnableTracing
		cfg.EnableTracing = true
		DeferCleanup(func() { cfg.EnableTracing = was })
		logs = captureLogs()
		DeferCleanup(logs.stop)
	})

	It("finds the sentinel of a normal chat, so the scan looks in the right places", func() {
		sentinel := newSentinel("NORMAL")
		id := chatsCreate()
		chatsSend(id, "Repeat exactly: "+sentinel)
		Eventually(func() bool { return chatsIdle(id) }, 60*time.Second, 200*time.Millisecond).Should(BeTrue())
		Expect(sentinelPlaces(sentinel, logs)).NotTo(BeEmpty())
	})

	DescribeTable("a zero-retention chat leaves the sentinel nowhere and keeps the usage counter",
		func(withAgent bool) {
			sentinel := newSentinel("ZERO")
			before := usageRequests()
			body := map[string]any{"model": "mock-model"}
			if withAgent {
				body["agent_id"] = agCreateAgent(agObj{"name": "incognito-" + strings.ToLower(sentinel[len(sentinel)-8:]), "instructions": "Answer briefly."})
			}
			id := incognitoCreate(body, privacy.NameZeroRetention)
			incognitoSend(id, "Repeat exactly: "+sentinel, privacy.NameZeroRetention)
			Eventually(func() bool { return chatsIdle(id) }, 60*time.Second, 200*time.Millisecond).Should(BeTrue())

			// The chat answered: the sentinel is in its transcript, in memory.
			Expect(userTexts(chatsTranscript(id))).To(ContainElement(ContainSubstring(sentinel)))
			// It is not listed.
			_, list := chatsDo(http.MethodGet, chatsBase()+"?limit=200", nil)
			Expect(string(list)).NotTo(ContainSubstring(id))

			Expect(sentinelPlaces(sentinel, logs)).To(BeEmpty())
			Eventually(usageRequests, 10*time.Second, 200*time.Millisecond).Should(BeNumerically(">", before), "the usage counter of the request is kept")

			// Discarding the chat ends it.
			code, _ := chatsDo(http.MethodDelete, chatsBase()+"/"+id, nil)
			Expect(code).To(Equal(http.StatusOK))
			code, _ = chatsDo(http.MethodGet, chatsBase()+"/"+id, nil)
			Expect(code).To(Equal(http.StatusNotFound))
			Expect(sentinelPlaces(sentinel, logs)).To(BeEmpty())
		},
		Entry("plain chat", false),
		Entry("chat with a saved agent", true),
	)

	It("keeps memory away from a memory-off chat, and a later chat cannot read its sentinel", func() {
		if code, body := chatsDo(http.MethodGet, chatsBase()+"/files", nil); code != http.StatusOK {
			Skip("files are off on this server, so there is no memory: " + string(body))
		}
		note := newSentinel("NOTE")
		putHomeFile("/Memory/index.md", "- "+note+"\n")
		DeferCleanup(func() {
			chatsDo(http.MethodDelete, chatsBase()+"/files?path="+url.QueryEscape("/Memory/index.md"), nil)
		})

		// Control: a normal chat reads the note, so the model request has it.
		id := chatsCreate()
		chatsSend(id, "hello")
		Eventually(func() bool { return chatsIdle(id) }, 60*time.Second, 200*time.Millisecond).Should(BeTrue())
		Expect(tracesContain(note)).To(BeTrue(), "a normal chat reads the memory note")
		clearTraces()

		secret := newSentinel("MEMOFF")
		mid := incognitoCreate(map[string]any{"model": "mock-model"}, privacy.NameMemoryOff)
		incognitoSend(mid, "Remember this for later: "+secret, privacy.NameMemoryOff)
		Eventually(func() bool { return chatsIdle(mid) }, 60*time.Second, 200*time.Millisecond).Should(BeTrue())
		// memory-off keeps traces, so the request is there: without the note.
		Expect(tracesContain(secret)).To(BeTrue(), "memory-off keeps its traces")
		Expect(tracesContain(note)).To(BeFalse(), "a memory-off chat does not read memory")
		code, _ := chatsDo(http.MethodDelete, chatsBase()+"/"+mid, nil)
		Expect(code).To(Equal(http.StatusOK))

		// Nothing of it reached memory: no note has the secret, and the next
		// normal chat does not get it in its prompt.
		Expect(homeFilesContain("/Memory", secret)).To(BeFalse())
		clearTraces()
		later := chatsCreate()
		chatsSend(later, "what did I ask you to remember?")
		Eventually(func() bool { return chatsIdle(later) }, 60*time.Second, 200*time.Millisecond).Should(BeTrue())
		Expect(tracesContain(note)).To(BeTrue(), "the later chat reads memory")
		Expect(tracesContain(secret)).To(BeFalse(), "the later chat cannot recall the memory-off sentinel")
	})
})

func newSentinel(kind string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "SENTINEL-" + kind + "-" + strings.ToUpper(hex.EncodeToString(b[:]))
}

func incognitoDo(method, url string, body any, level string) (int, []byte) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		Expect(err).ToNot(HaveOccurred())
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	Expect(err).ToNot(HaveOccurred())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(privacy.Header, level)
	resp, err := http.DefaultClient.Do(req)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	return resp.StatusCode, out
}

func incognitoCreate(body map[string]any, level string) string {
	code, out := incognitoDo(http.MethodPost, chatsBase(), body, level)
	ExpectWithOffset(1, code).To(Equal(http.StatusCreated), string(out))
	var c struct {
		ID        string `json:"id"`
		Incognito string `json:"incognito"`
	}
	ExpectWithOffset(1, json.Unmarshal(out, &c)).To(Succeed())
	ExpectWithOffset(1, c.Incognito).To(Equal(level))
	return c.ID
}

func incognitoSend(id, text, level string) {
	code, out := incognitoDo(http.MethodPost, chatsBase()+"/"+id+"/messages", map[string]string{"text": text}, level)
	ExpectWithOffset(1, code).To(Equal(http.StatusOK), string(out))
}

func usageRequests() int64 {
	code, out := chatsDo(http.MethodGet, strings.TrimSuffix(apiURL, "/v1")+"/api/usage?period=day", nil)
	Expect(code).To(Equal(http.StatusOK), string(out))
	var r struct {
		Totals struct {
			RequestCount int64 `json:"request_count"`
		} `json:"totals"`
	}
	Expect(json.Unmarshal(out, &r)).To(Succeed())
	return r.Totals.RequestCount
}

func putHomeFile(path, text string) {
	req, err := http.NewRequest(http.MethodPut, chatsBase()+"/files/content?path="+url.QueryEscape(path), strings.NewReader(text))
	Expect(err).ToNot(HaveOccurred())
	resp, err := http.DefaultClient.Do(req)
	Expect(err).ToNot(HaveOccurred())
	_ = resp.Body.Close()
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
}

// homeFilesContain searches the person's files under dir.
func homeFilesContain(dir, text string) bool {
	code, out := chatsDo(http.MethodGet, chatsBase()+"/files/search?q="+url.QueryEscape(text), nil)
	Expect(code).To(Equal(http.StatusOK), string(out))
	return strings.Contains(string(out), dir)
}

func clearTraces() {
	for _, p := range []string{"/api/traces/clear", "/api/backend-traces/clear"} {
		code, _ := chatsDo(http.MethodPost, strings.TrimSuffix(apiURL, "/v1")+p, nil)
		Expect(code).To(BeNumerically("<", 300))
	}
}

// tracesContain reads the API and backend traces the server holds.
func tracesContain(text string) bool {
	root := strings.TrimSuffix(apiURL, "/v1")
	for _, p := range []string{"/api/traces?full=true&limit=1000", "/api/backend-traces?full=true&limit=1000"} {
		code, out := chatsDo(http.MethodGet, root+p, nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		if bytes.Contains(out, []byte(text)) {
			return true
		}
	}
	return false
}

// sentinelPlaces lists every place that holds text: a file under the data
// path, the generated content or the models, a trace, or a log line.
func sentinelPlaces(text string, logs *logCapture) []string {
	var found []string
	cfg := localAIApp.ApplicationConfig()
	roots := []string{cfg.DataPath, cfg.GeneratedContentDir, modelsPath}
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, err := d.Info(); err != nil || info.Size() > 256<<20 {
				return nil
			}
			b, err := os.ReadFile(path)
			if err == nil && bytes.Contains(b, []byte(text)) {
				found = append(found, "file "+path)
			}
			return nil
		})
	}
	if tracesContain(text) {
		found = append(found, "traces")
	}
	if line := logs.find(text); line != "" {
		found = append(found, "log: "+line)
	}
	return found
}

// logCapture holds what the server logs while a spec runs: the xlog logger
// writes to the standard output, the chats to the standard log.
type logCapture struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	r, w   *os.File
	stdout *os.File
	done   chan struct{}
}

func captureLogs() *logCapture {
	r, w, err := os.Pipe()
	Expect(err).ToNot(HaveOccurred())
	c := &logCapture{r: r, w: w, stdout: os.Stdout, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		chunk := make([]byte, 32<<10)
		for {
			n, err := r.Read(chunk)
			if n > 0 {
				c.mu.Lock()
				c.buf.Write(chunk[:n])
				c.mu.Unlock()
				_, _ = c.stdout.Write(chunk[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	os.Stdout = w
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("debug"), "text"))
	log.SetOutput(w)
	return c
}

func (c *logCapture) stop() {
	os.Stdout = c.stdout
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(os.Getenv(xlog.EnvLogLevel)), os.Getenv(xlog.EnvLogFormat)))
	log.SetOutput(os.Stderr)
	_ = c.w.Close()
	<-c.done
	_ = c.r.Close()
}

// find returns the first captured line that holds text.
func (c *logCapture) find(text string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range strings.Split(c.buf.String(), "\n") {
		if strings.Contains(line, text) {
			return fmt.Sprintf("%.300s", line)
		}
	}
	return ""
}
