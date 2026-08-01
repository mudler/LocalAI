package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// modelServer answers /v1/models with the given ids, as LocalAI does.
func modelServer(ids ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json")
		Expect(json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})).To(Succeed())
	}))
}

var _ = Describe("prepare", func() {
	var (
		dir    string
		errOut *bytes.Buffer
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		errOut = &bytes.Buffer{}
	})

	// optionsFor points a run at srv, with no input to read: the default is a
	// session nobody can be asked anything in.
	optionsFor := func(srv *httptest.Server) Options {
		endpoint := "http://127.0.0.1:0"
		base := endpoint + "/v1"
		if srv != nil {
			endpoint, base = srv.URL, srv.URL+"/v1"
		}
		return Options{
			Endpoint: endpoint,
			BaseURL:  base,
			StateDir: dir,
			In:       strings.NewReader(""),
			Out:      &bytes.Buffer{},
			ErrOut:   errOut,
		}
	}

	It("uses the only model the server offers", func() {
		srv := modelServer("the-only-model")
		defer srv.Close()

		p, err := prepare(context.Background(), optionsFor(srv), false)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.model).To(Equal("the-only-model"))
		Expect(p.dir).To(Equal(dir))
		Expect(p.server).To(BeNil(), "nothing was started, so nothing is owned")
	})

	It("seeds the agent config with the endpoint on first run", func() {
		srv := modelServer("m")
		defer srv.Close()

		_, err := prepare(context.Background(), optionsFor(srv), false)
		Expect(err).ToNot(HaveOccurred())

		data, err := os.ReadFile(ConfigPath(dir))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(srv.URL + "/v1"))
	})

	It("lets --model win over what the server offers", func() {
		srv := modelServer("a", "b")
		defer srv.Close()

		opts := optionsFor(srv)
		opts.Model = "not-listed-yet"
		p, err := prepare(context.Background(), opts, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.model).To(Equal("not-listed-yet"))
	})

	It("advises about the API key when the server rejects it", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		_, err := prepare(context.Background(), optionsFor(srv), false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("--api-key"))
		Expect(err.Error()).To(ContainSubstring(srv.URL))
	})

	// Not interactive means nobody can answer the offer, so the advice has to
	// stand on its own.
	It("advises how to start a server when none is reachable", func() {
		srv := modelServer()
		url := srv.URL
		srv.Close() // nothing is listening now

		opts := optionsFor(nil)
		opts.Endpoint, opts.BaseURL = url, url+"/v1"
		_, err := prepare(context.Background(), opts, false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("local-ai run"))
		Expect(err.Error()).To(ContainSubstring(url))
	})

	// A server that accepts the connection and then never replies is the case
	// the offer to start one exists for, so the budget has to expire as a
	// deadline: Probe reads a cancellation as "the caller gave up" and refuses
	// to call the endpoint unreachable on the strength of it.
	It("treats a server that never answers as one that is not there", func(ctx SpecContext) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		defer close(release)

		opts := optionsFor(srv)
		opts.ProbeTimeout = 100 * time.Millisecond
		_, err := prepare(context.Background(), opts, false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("local-ai run"), "want the offer-a-server advice, got %v", err)
	}, SpecTimeout(30*time.Second))

	It("asks which model to use and remembers the answer", func() {
		srv := modelServer("zeta", "alpha")
		defer srv.Close()

		opts := optionsFor(srv)
		opts.In = strings.NewReader("2\n")
		p, err := prepare(context.Background(), opts, true)
		Expect(err).ToNot(HaveOccurred())
		// The list is sorted before it is shown, so 2 is zeta, not the second
		// thing the server happened to name.
		Expect(p.model).To(Equal("zeta"))
		Expect(errOut.String()).To(ContainSubstring("1) alpha"))
		Expect(errOut.String()).To(ContainSubstring("2) zeta"))

		data, err := os.ReadFile(ConfigPath(dir))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("zeta"))
	})

	// The choice is prompted for once and remembered. When remembering it fails
	// the user is about to be asked again on every future run, so they have to
	// be told here: a log line is invisible at the default log level.
	It("says so on the prompt when the choice cannot be remembered", func() {
		srv := modelServer("zeta", "alpha")
		defer srv.Close()

		// A directory where the config file belongs: writable state dir,
		// unwritable config, on any platform and as any user.
		Expect(os.MkdirAll(ConfigPath(dir), 0o700)).To(Succeed())

		opts := optionsFor(srv)
		opts.In = strings.NewReader("1\n")
		p, err := prepare(context.Background(), opts, true)

		// Failing to remember the choice must not cost the user their session.
		Expect(err).ToNot(HaveOccurred())
		Expect(p.model).To(Equal("alpha"))
		Expect(errOut.String()).To(ContainSubstring("could not be saved"), "the user has to learn they will be asked again")
	})

	It("does not ask again once a model is recorded", func() {
		srv := modelServer("zeta", "alpha")
		defer srv.Close()

		Expect(PersistModel(dir, "alpha")).To(Succeed())

		opts := optionsFor(srv)
		opts.In = strings.NewReader("") // an answer would have nothing to read
		p, err := prepare(context.Background(), opts, true)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.model).To(Equal("alpha"))
		Expect(errOut.String()).To(BeEmpty())
	})

	It("says what to install when the server has no models", func() {
		srv := modelServer()
		defer srv.Close()

		_, err := prepare(context.Background(), optionsFor(srv), false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("models install"))
	})

	Describe("arguments that only touch local state", func() {
		unreachable := func(args ...string) Options {
			opts := optionsFor(nil) // port 0: nothing can ever answer here
			opts.Args = args
			return opts
		}

		DescribeTable("skips the server entirely",
			func(args ...string) {
				p, err := prepare(context.Background(), unreachable(args...), false)
				Expect(err).ToNot(HaveOccurred())
				Expect(p.model).To(BeEmpty())
				Expect(p.server).To(BeNil())
			},
			Entry("plugin", "plugin", "list"),
			Entry("skill", "skill", "list"),
			Entry("mcp add", "mcp", "add", "srv"),
		)
	})

	// A reader per question would read ahead into a buffer it then discards, so
	// the second question would see EOF whenever both answers were typed ahead.
	// That is the shape of a real run: the offer to start a server is followed
	// by the model prompt.
	It("keeps reading answers from the same stream across questions", func() {
		out := &bytes.Buffer{}
		p := newPrompter(strings.NewReader("y\n2\n"), out)

		yes, err := p.yesNo("Start one now?")
		Expect(err).ToNot(HaveOccurred())
		Expect(yes).To(BeTrue())

		chosen, err := p.choose([]string{"alpha", "zeta"})
		Expect(err).ToNot(HaveOccurred())
		Expect(chosen).To(Equal("zeta"))
	})

	// Whatever the chooser returns is persisted and used for every later run,
	// so an answer that is not one of the offered models must never come back
	// as one.
	Describe("the model prompt", func() {
		offered := []string{"alpha", "zeta"}

		DescribeTable("refuses an answer that is not one of the numbers shown",
			func(answer string) {
				chosen, err := newPrompter(strings.NewReader(answer), &bytes.Buffer{}).choose(offered)
				Expect(err).To(HaveOccurred())
				Expect(chosen).To(BeEmpty())
			},
			Entry("nothing at all", ""),
			Entry("a blank line", "\n"),
			Entry("only spaces", "   \n"),
			Entry("zero", "0\n"),
			Entry("past the end", "3\n"),
			Entry("negative", "-1\n"),
			Entry("a model name", "zeta\n"),
			Entry("a number with a suffix", "1x\n"),
		)

		It("says how to answer when the answer was not a number", func() {
			_, err := newPrompter(strings.NewReader("banana\n"), &bytes.Buffer{}).choose(offered)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("between 1 and 2"))
			Expect(err.Error()).To(ContainSubstring("--model"))
		})

		It("returns the model shown against the number", func() {
			chosen, err := newPrompter(strings.NewReader("1\n"), &bytes.Buffer{}).choose(offered)
			Expect(err).ToNot(HaveOccurred())
			Expect(chosen).To(Equal("alpha"))
		})

		It("refuses to ask when there is nothing to offer", func() {
			chosen, err := newPrompter(strings.NewReader("1\n"), &bytes.Buffer{}).choose(nil)
			Expect(err).To(HaveOccurred())
			Expect(chosen).To(BeEmpty())
		})
	})

	It("reports a state dir it cannot create", func() {
		blocked := filepath.Join(dir, "a-file")
		Expect(os.WriteFile(blocked, []byte("not a dir"), 0o600)).To(Succeed())

		opts := optionsFor(nil)
		opts.StateDir = filepath.Join(blocked, "chat")
		_, err := prepare(context.Background(), opts, false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("agent state dir"))
	})
})
