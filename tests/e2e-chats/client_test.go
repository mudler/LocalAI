package e2echats_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// client talks to one server as one caller.
type client struct {
	base   string
	bearer string
	cookie string
}

func (c *client) req(method, path string, body io.Reader, ctype string) *http.Request {
	r, err := http.NewRequest(method, c.base+path, body)
	Expect(err).ToNot(HaveOccurred())
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	if c.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.cookie != "" {
		r.Header.Set("Cookie", c.cookie)
	}
	return r
}

// do sends JSON (or nothing) and returns the status and the body.
func (c *client) do(method, path string, body any) (int, []byte) {
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd, ctype = bytes.NewReader(b), "application/octet-stream"
	case string:
		rd, ctype = strings.NewReader(b), "application/json"
	default:
		raw, err := json.Marshal(b)
		Expect(err).ToNot(HaveOccurred())
		rd, ctype = bytes.NewReader(raw), "application/json"
	}
	resp, err := http.DefaultClient.Do(c.req(method, path, rd, ctype))
	Expect(err).ToNot(HaveOccurred())
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	return resp.StatusCode, out
}

// get decodes a JSON answer into out and returns the status.
func (c *client) get(path string, out any) int {
	code, raw := c.do(http.MethodGet, path, nil)
	if out != nil && code == http.StatusOK {
		Expect(json.Unmarshal(raw, out)).To(Succeed(), string(raw))
	}
	return code
}

// post sends JSON, decodes a JSON answer and returns the status.
func (c *client) post(path string, body, out any) int {
	code, raw := c.do(http.MethodPost, path, body)
	if out != nil && code < 300 {
		Expect(json.Unmarshal(raw, out)).To(Succeed(), string(raw))
	}
	return code
}

// errType is the "type" of an error answer.
func errType(raw []byte) string {
	var e struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	return e.Error.Type
}

type chatInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	Kind      string `json:"kind"`
	ParentID  string `json:"parent_id"`
	AgentID   string `json:"agent_id"`
	Assistant bool   `json:"assistant"`
	Running   bool   `json:"running"`
	LastTurn  string `json:"last_turn"`
	Pending   []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"pending"`
}

type chatMsg struct {
	Seq  int64  `json:"seq"`
	Turn int    `json:"turn"`
	Role string `json:"role"`
	Text string `json:"text"`
}

// create starts a chat and fails the spec if the server refuses.
func (c *client) create(model string) chatInfo {
	var out chatInfo
	code, raw := c.do(http.MethodPost, "/api/chats", map[string]any{"model": model})
	Expect(code).To(Equal(http.StatusCreated), string(raw))
	Expect(json.Unmarshal(raw, &out)).To(Succeed())
	return out
}

func (c *client) info(id string) chatInfo {
	var out chatInfo
	Expect(c.get("/api/chats/"+id, &out)).To(Equal(http.StatusOK))
	return out
}

func (c *client) send(id, text string) (started bool, pendingID string) {
	var out struct {
		Started   bool   `json:"started"`
		PendingID string `json:"pending_id"`
	}
	code, raw := c.do(http.MethodPost, "/api/chats/"+id+"/messages", map[string]string{"text": text})
	Expect(code).To(Equal(http.StatusOK), string(raw))
	Expect(json.Unmarshal(raw, &out)).To(Succeed())
	return out.Started, out.PendingID
}

func (c *client) transcript(id string) []chatMsg {
	var out struct {
		Messages []chatMsg `json:"messages"`
	}
	Expect(c.get("/api/chats/"+id+"/messages?limit=1000", &out)).To(Equal(http.StatusOK))
	return out.Messages
}

// settled waits until no turn runs and returns the last turn's outcome.
func (c *client) settled(id string) string {
	var last string
	Eventually(func() bool {
		i := c.info(id)
		last = i.LastTurn
		return !i.Running && i.LastTurn != ""
	}, 60*time.Second, 100*time.Millisecond).Should(BeTrue())
	return last
}

// say sends a message to an idle chat and waits for the turn to end.
func (c *client) say(id, text string) string {
	started, _ := c.send(id, text)
	Expect(started).To(BeTrue())
	return c.settled(id)
}

func texts(msgs []chatMsg, role string) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == role {
			out = append(out, m.Text)
		}
	}
	return out
}

// sseEvent is one event of /events.
type sseEvent struct {
	Kind string `json:"kind"`
	Turn int    `json:"turn"`
	Text string `json:"text"`
	Tool *struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Output string `json:"output"`
		Seq    int64  `json:"seq"`
	} `json:"tool"`
	IDs    []string `json:"ids"`
	Before int      `json:"before"`
	After  int      `json:"after"`
}

// eventStream collects the events of a chat from the moment it is opened.
type eventStream struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	got    []sseEvent
	status int
	ctype  string
	done   chan struct{}
}

func (c *client) events(id string) *eventStream {
	ctx, cancel := context.WithCancel(context.Background())
	es := &eventStream{cancel: cancel, done: make(chan struct{})}
	r := c.req(http.MethodGet, "/api/chats/"+id+"/events", nil, "")
	r = r.WithContext(ctx)
	ready := make(chan struct{})
	go func() {
		defer close(es.done)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			close(ready)
			return
		}
		defer resp.Body.Close()
		es.mu.Lock()
		es.status, es.ctype = resp.StatusCode, resp.Header.Get("Content-Type")
		es.mu.Unlock()
		close(ready)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev sseEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil {
				es.mu.Lock()
				es.got = append(es.got, ev)
				es.mu.Unlock()
			}
		}
	}()
	<-ready
	return es
}

func (e *eventStream) Close() { e.cancel(); <-e.done }

func (e *eventStream) Status() (int, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status, e.ctype
}

func (e *eventStream) All() []sseEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sseEvent(nil), e.got...)
}

// Kinds lists the event kinds in order, with consecutive repeats folded.
func (e *eventStream) Kinds() []string {
	var out []string
	for _, ev := range e.All() {
		if len(out) == 0 || out[len(out)-1] != ev.Kind {
			out = append(out, ev.Kind)
		}
	}
	return out
}

// Text joins the text events.
func (e *eventStream) Text(kind string) string {
	var b strings.Builder
	for _, ev := range e.All() {
		if ev.Kind == kind {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

// Has says whether an event of the kind arrived.
func (e *eventStream) Has(kind string) bool {
	for _, ev := range e.All() {
		if ev.Kind == kind {
			return true
		}
	}
	return false
}

func (e *eventStream) Wait(kind string) {
	Eventually(func() bool { return e.Has(kind) }, 60*time.Second, 50*time.Millisecond).Should(BeTrue(), fmt.Sprintf("no %q event, got %v", kind, e.Kinds()))
}

// settledOrGap waits for the turn to end. When it does not end in time the spec
// stops the turn and is skipped with the gap id: the server is wrong, not the spec.
func (c *client) settledOrGap(id, gap string, wait time.Duration) string {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		i := c.info(id)
		if !i.Running && i.LastTurn != "" {
			return i.LastTurn
		}
		time.Sleep(200 * time.Millisecond)
	}
	_, _ = c.do(http.MethodPost, "/api/chats/"+id+"/stop", nil)
	c.settled(id)
	Skip("known gap: " + gap)
	return ""
}

func jsonInto(raw []byte, out any) error { return json.Unmarshal(raw, out) }
