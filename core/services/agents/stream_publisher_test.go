package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// chunkWriter is an http.ResponseWriter's concurrency contract and nothing
// else: it is NOT safe for concurrent use, and it splits every Write into
// single bytes with a scheduling point between them.
//
// Both halves are deliberate. A double that locked internally would make two
// unsynchronised Encode calls look atomic and could never fail the way a real
// response fails, which is the failure this spec exists to catch. Splitting the
// write is what turns "two goroutines might interleave" into "two goroutines
// do", so the concurrency spec reddens on the framing itself and not only under
// the race detector.
type chunkWriter struct {
	buf []byte
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		w.buf = append(w.buf, b)
		runtime.Gosched()
	}
	return len(p), nil
}

// errWriter fails every write, for the one thing Publish promises about a
// response it cannot write on.
type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

// decodeLines reads every envelope out of a buffer, which is the assertion that
// fails on interleaving: a torn line does not decode, and a line that swallowed
// its neighbour does not produce two.
func decodeLines(raw []byte) ([]workerctl.Envelope, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var out []workerctl.Envelope
	for {
		var env workerctl.Envelope
		err := dec.Decode(&env)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, env)
	}
}

var _ = Describe("StreamPublisher", func() {
	var (
		buf     *bytes.Buffer
		flushes int
		pub     *StreamPublisher
	)

	BeforeEach(func() {
		buf = &bytes.Buffer{}
		flushes = 0
		pub = NewStreamPublisher(buf, func() { flushes++ })
	})

	It("writes exactly one NDJSON line whose subject and progress decode back to the inputs", func() {
		Expect(pub.Publish("agent.a1.events.status", map[string]any{"state": "thinking"})).To(Succeed())

		lines, err := decodeLines(buf.Bytes())
		Expect(err).ToNot(HaveOccurred())
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Subject).To(Equal("agent.a1.events.status"))
		Expect(string(lines[0].Progress)).To(MatchJSON(`{"state":"thinking"}`))
		// A line the frontend would read as terminal ends the stream, so a
		// progress publisher must never write one.
		Expect(lines[0].Reply).To(BeNil())
		Expect(bytes.Count(buf.Bytes(), []byte("\n"))).To(Equal(1))
	})

	It("flushes after every line, so a tick reaches the frontend while the handler still runs", func() {
		Expect(pub.Publish("jobs.j1.progress", map[string]any{"percentage": 10})).To(Succeed())
		Expect(flushes).To(Equal(1))
		Expect(pub.Publish("jobs.j1.progress", map[string]any{"percentage": 20})).To(Succeed())
		Expect(flushes).To(Equal(2))
	})

	It("writes a line at all when it was given no flush to call", func() {
		plain := &bytes.Buffer{}
		Expect(NewStreamPublisher(plain, nil).Publish("jobs.j1.result", map[string]any{"ok": true})).To(Succeed())
		lines, err := decodeLines(plain.Bytes())
		Expect(err).ToNot(HaveOccurred())
		Expect(lines).To(HaveLen(1))
	})

	It("keeps every concurrent publish a complete, decodable line of its own", func() {
		w := &chunkWriter{}
		p := NewStreamPublisher(w, func() {})

		// Four writers rather than two, and enough lines each that an
		// unsynchronised run has to interleave rather than merely be allowed
		// to. Measured: at two writers of twenty lines an unlocked publisher
		// still produced a decodable buffer on some schedules, so the framing
		// assertion below was a coin toss and only the race detector was
		// reliably red.
		subjects := []string{
			"agent.a1.events.status",
			"agent.a2.events.status",
			"jobs.j1.progress",
			"jobs.j2.result",
		}
		const perGoroutine = 60
		var wg sync.WaitGroup
		errs := make(chan error, len(subjects)*perGoroutine)
		for _, subject := range subjects {
			wg.Add(1)
			go func(subject string) {
				defer wg.Done()
				for i := 0; i < perGoroutine; i++ {
					if err := p.Publish(subject, map[string]any{"n": i, "subject": subject}); err != nil {
						errs <- err
					}
				}
			}(subject)
		}
		wg.Wait()
		close(errs)
		Expect(errs).ToNot(Receive())

		lines, err := decodeLines(w.buf)
		Expect(err).ToNot(HaveOccurred(), "a line was torn by an interleaved write")
		Expect(lines).To(HaveLen(len(subjects) * perGoroutine))
		// Every line must still carry the pair it was written with. A decode
		// that happened to succeed on spliced bytes would show up here as a
		// subject that does not match its payload.
		for _, line := range lines {
			var payload struct {
				Subject string `json:"subject"`
			}
			Expect(json.Unmarshal(line.Progress, &payload)).To(Succeed())
			Expect(payload.Subject).To(Equal(line.Subject))
		}
	})

	It("reports a value it cannot encode without writing anything", func() {
		Expect(pub.Publish("jobs.j1.progress", make(chan int))).ToNot(Succeed())
		Expect(buf.Len()).To(BeZero())
		Expect(flushes).To(BeZero())
	})

	It("reports a response it cannot write on, and does not flush a line it did not write", func() {
		failing := NewStreamPublisher(errWriter{err: errors.New("connection reset")}, func() { flushes++ })
		Expect(failing.Publish("jobs.j1.progress", map[string]any{"percentage": 10})).ToNot(Succeed())
		Expect(flushes).To(BeZero())
	})
})
