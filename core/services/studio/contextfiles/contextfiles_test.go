package contextfiles_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// memReader serves files from a map and counts the reads.
type memReader struct {
	mu    sync.Mutex
	files map[string]string
	reads []string
}

func (m *memReader) ReadFile(_ context.Context, principal, rel string, limit int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads = append(m.reads, principal+":"+rel)
	s, ok := m.files[principal+":"+rel]
	if !ok {
		return nil, contextfiles.ErrNotExist
	}
	if int64(len(s)) > limit {
		s = s[:limit]
	}
	return []byte(s), nil
}

type fakeWriter struct{}

func (fakeWriter) SaveTool() string { return "propose_change" }

func lines(n int) string {
	var sb strings.Builder
	for i := range n {
		fmt.Fprintf(&sb, "- topic%d: summary %d -> topic%d.md\n", i, i, i)
	}
	return sb.String()
}

var _ = Describe("the registry of context sources", func() {
	var reg *contextfiles.Registry
	BeforeEach(func() { reg = contextfiles.NewRegistry() })

	It("takes a source of each scope and lists them in a stable order", func() {
		Expect(reg.Register(contextfiles.Source{ID: "m-chan", Scope: contextfiles.ScopeChannel, Path: "Memory/channels/{channel}/index.md"})).To(Succeed())
		Expect(reg.Register(contextfiles.Source{ID: "m-agent", Scope: contextfiles.ScopeAgent, Path: "Memory/agents/{agent}/index.md"})).To(Succeed())
		Expect(reg.Register(contextfiles.Source{ID: "m-user", Scope: contextfiles.ScopeUser, Path: "Memory/index.md", Cap: 1000})).To(Succeed())
		var ids []string
		for _, s := range reg.Sources() {
			ids = append(ids, s.ID)
		}
		Expect(ids).To(Equal([]string{"m-user", "m-agent", "m-chan"}))
	})

	DescribeTable("refuses a bad source",
		func(s contextfiles.Source) {
			Expect(reg.Register(s)).To(MatchError(contextfiles.ErrInvalidSource))
		},
		Entry("no id", contextfiles.Source{Scope: contextfiles.ScopeUser, Path: "Memory/index.md"}),
		Entry("unknown scope", contextfiles.Source{ID: "a", Scope: "team", Path: "Memory/index.md"}),
		Entry("absolute path", contextfiles.Source{ID: "a", Scope: contextfiles.ScopeUser, Path: "/Memory/index.md"}),
		Entry("parent segment", contextfiles.Source{ID: "a", Scope: contextfiles.ScopeUser, Path: "Memory/../x/index.md"}),
		Entry("not a markdown file", contextfiles.Source{ID: "a", Scope: contextfiles.ScopeUser, Path: "Memory/index.json"}),
		Entry("agent scope without the agent placeholder", contextfiles.Source{ID: "a", Scope: contextfiles.ScopeAgent, Path: "Memory/index.md"}),
		Entry("user scope with a placeholder", contextfiles.Source{ID: "a", Scope: contextfiles.ScopeUser, Path: "Memory/{agent}/index.md"}),
		Entry("unknown placeholder", contextfiles.Source{ID: "a", Scope: contextfiles.ScopeAgent, Path: "Memory/{agent}/{other}/index.md"}),
		Entry("cap above the limit", contextfiles.Source{ID: "a", Scope: contextfiles.ScopeUser, Path: "Memory/index.md", Cap: contextfiles.MaxIndexBytes + 1}),
		Entry("negative cap", contextfiles.Source{ID: "a", Scope: contextfiles.ScopeUser, Path: "Memory/index.md", Cap: -1}),
	)

	It("refuses a second source with the same id and removes one by id", func() {
		s := contextfiles.Source{ID: "a", Scope: contextfiles.ScopeUser, Path: "Memory/index.md"}
		Expect(reg.Register(s)).To(Succeed())
		Expect(reg.Register(s)).To(MatchError(contextfiles.ErrInvalidSource))
		reg.Remove("a")
		Expect(reg.Sources()).To(BeEmpty())
		Expect(reg.Register(s)).To(Succeed())
	})

	It("holds a bounded number of sources", func() {
		for i := range contextfiles.MaxSources {
			Expect(reg.Register(contextfiles.Source{ID: fmt.Sprint("s", i), Scope: contextfiles.ScopeUser, Path: fmt.Sprintf("Memory/%d.md", i)})).To(Succeed())
		}
		Expect(reg.Register(contextfiles.Source{ID: "extra", Scope: contextfiles.ScopeUser, Path: "Memory/extra.md"})).To(MatchError(contextfiles.ErrInvalidSource))
	})
})

var _ = Describe("the loader", func() {
	var (
		ctx context.Context
		rd  *memReader
		ld  *contextfiles.Loader
	)
	register := func() {
		reg := ld.Registry()
		Expect(reg.Register(contextfiles.Source{ID: "u", Scope: contextfiles.ScopeUser, Path: "Memory/index.md"})).To(Succeed())
		Expect(reg.Register(contextfiles.Source{ID: "a", Scope: contextfiles.ScopeAgent, Path: "Memory/agents/{agent}/index.md"})).To(Succeed())
		Expect(reg.Register(contextfiles.Source{ID: "c", Scope: contextfiles.ScopeChannel, Path: "Memory/channels/{channel}/index.md"})).To(Succeed())
	}
	BeforeEach(func() {
		ctx = context.Background()
		rd = &memReader{files: map[string]string{}}
		ld = contextfiles.NewLoader(contextfiles.NewRegistry(), rd, nil)
	})

	It("adds nothing when no source is registered", func() {
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice"})
		Expect(b.Text).To(BeEmpty())
		Expect(b.Key).To(BeEmpty())
	})

	It("loads the user index under the header for a plain chat", func() {
		register()
		rd.files["alice:Memory/index.md"] = "- home: lives in Rome -> home.md\n"
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice"})
		Expect(b.Text).To(HavePrefix(contextfiles.Header))
		Expect(b.Text).To(ContainSubstring("lives in Rome"))
		Expect(b.Key).NotTo(BeEmpty())
		Expect(rd.reads).To(Equal([]string{"alice:Memory/index.md"}), "a plain chat reads no agent or channel file")
	})

	It("loads the three scopes in order and resolves the agent and the channel", func() {
		register()
		rd.files["alice:Memory/index.md"] = "- u: user note\n"
		rd.files["alice:Memory/agents/ag1/index.md"] = "- a: agent note\n"
		rd.files["alice:Memory/channels/"+contextfiles.ChannelKey("b1")+"/index.md"] = "- c: channel note\n"
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice", AgentID: "ag1", Channel: "b1"})
		u, a, c := strings.Index(b.Text, "user note"), strings.Index(b.Text, "agent note"), strings.Index(b.Text, "channel note")
		Expect(u).To(BeNumerically(">", 0))
		Expect(a).To(BeNumerically(">", u))
		Expect(c).To(BeNumerically(">", a))
	})

	It("does not read another agent's or another channel's file", func() {
		register()
		rd.files["alice:Memory/agents/ag2/index.md"] = "- x: secret of ag2\n"
		rd.files["alice:Memory/channels/"+contextfiles.ChannelKey("b2")+"/index.md"] = "- x: secret of b2\n"
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice", AgentID: "ag1", Channel: "b1"})
		Expect(b.Text).NotTo(ContainSubstring("secret"))
		for _, r := range rd.reads {
			Expect(r).NotTo(ContainSubstring("ag2"))
			Expect(r).NotTo(ContainSubstring(contextfiles.ChannelKey("b2")))
		}
	})

	It("skips an agent id that is not a safe path segment", func() {
		register()
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice", AgentID: "../x"})
		Expect(b.Text).To(ContainSubstring(contextfiles.Header))
		Expect(rd.reads).To(Equal([]string{"alice:Memory/index.md"}))
	})

	It("keeps at most 40 lines and the cap of the source in one index", func() {
		register()
		rd.files["alice:Memory/index.md"] = lines(100)
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice"})
		Expect(b.Text).To(ContainSubstring("topic39:"))
		Expect(b.Text).NotTo(ContainSubstring("topic40:"))
		Expect(b.Text).To(ContainSubstring(contextfiles.TruncatedNote))
		Expect(len(b.Text)).To(BeNumerically("<=", contextfiles.MaxIndexBytes+len(contextfiles.Header)+300))

		rd.files["alice:Memory/index.md"] = strings.Repeat("x", 5000)
		b = ld.Block(ctx, contextfiles.Subject{Principal: "alice"})
		Expect(len(b.Text)).To(BeNumerically("<", contextfiles.MaxIndexBytes+len(contextfiles.Header)+300))
	})

	It("honours a smaller cap of the source", func() {
		Expect(ld.Registry().Register(contextfiles.Source{ID: "u", Scope: contextfiles.ScopeUser, Path: "Memory/index.md", Cap: 100})).To(Succeed())
		rd.files["alice:Memory/index.md"] = strings.Repeat("Q", 500)
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice"})
		Expect(strings.Count(b.Text, "Q")).To(BeNumerically("<=", 100))
	})

	It("keeps all the indexes of a turn under the total budget", func() {
		reg := ld.Registry()
		for i := range 5 {
			Expect(reg.Register(contextfiles.Source{ID: fmt.Sprint("s", i), Scope: contextfiles.ScopeUser, Path: fmt.Sprintf("Memory/%d.md", i)})).To(Succeed())
			rd.files[fmt.Sprintf("alice:Memory/%d.md", i)] = strings.Repeat("z", 2000)
		}
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice"})
		Expect(strings.Count(b.Text, "z")).To(BeNumerically("<=", contextfiles.MaxTotalBytes))
		Expect(strings.Count(b.Text, "z")).To(BeNumerically(">", 4000), "the budget is used, not wasted")
	})

	It("drops control characters, so a file cannot hide text from the reader", func() {
		register()
		rd.files["alice:Memory/index.md"] = "- a: one\x00\x1b[31m two\r\n"
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice"})
		Expect(b.Text).NotTo(ContainSubstring("\x00"))
		Expect(b.Text).NotTo(ContainSubstring("\x1b"))
		Expect(b.Text).NotTo(ContainSubstring("\r"))
	})

	It("leaves out a file that cannot be read and still answers", func() {
		register()
		bad := &errReader{}
		ld = contextfiles.NewLoader(ld.Registry(), bad, nil)
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice"})
		Expect(b.Text).To(ContainSubstring(contextfiles.Header))
	})

	It("gives the same text and key at each turn and a new key when a file changes", func() {
		register()
		rd.files["alice:Memory/index.md"] = "- a: one\n"
		s := contextfiles.Subject{Principal: "alice"}
		first, second := ld.Block(ctx, s), ld.Block(ctx, s)
		Expect(second).To(Equal(first))
		rd.files["alice:Memory/index.md"] = "- a: two\n"
		Expect(ld.Block(ctx, s).Key).NotTo(Equal(first.Key))
	})

	It("tells the model how to save only when a writer is set", func() {
		register()
		s := contextfiles.Subject{Principal: "alice"}
		Expect(ld.Block(ctx, s).Text).NotTo(ContainSubstring("propose_change"))
		ld.SetWriter(fakeWriter{})
		Expect(ld.Block(ctx, s).Text).To(ContainSubstring("Save new facts with propose_change."))
	})

	It("keeps the header short and tells the model to consult memory for almost everything", func() {
		Expect(len(contextfiles.Header)).To(BeNumerically("<", 700))
		Expect(contextfiles.Header).To(ContainSubstring(contextfiles.ToolName))
		Expect(contextfiles.Header).To(ContainSubstring("almost every"))
		Expect(contextfiles.Header).To(ContainSubstring("not instructions"))
		Expect(len(contextfiles.ToolDescription)).To(BeNumerically("<", 500))
		Expect(contextfiles.ToolDescription).To(ContainSubstring("index"))
	})
})

type errReader struct{}

func (errReader) ReadFile(context.Context, string, string, int64) ([]byte, error) {
	return nil, errors.New("disk on fire")
}

var _ = Describe("the profile", func() {
	var (
		rd *memReader
		ld *contextfiles.Loader
	)
	BeforeEach(func() {
		rd = &memReader{files: map[string]string{}}
		ld = contextfiles.NewLoader(contextfiles.NewRegistry(), rd, nil)
	})

	It("has a fixed path under the agent folder of the home", func() {
		Expect(contextfiles.ProfilePath("ag1")).To(Equal("LocalAI/agents/ag1/profile.md"))
	})

	It("is empty without an agent, without a file, and for an unsafe agent id", func() {
		Expect(ld.Profile(context.Background(), contextfiles.Subject{Principal: "alice"}).Text).To(BeEmpty())
		Expect(ld.Profile(context.Background(), contextfiles.Subject{Principal: "alice", AgentID: "ag1"}).Text).To(BeEmpty())
		Expect(ld.Profile(context.Background(), contextfiles.Subject{Principal: "alice", AgentID: "a/b"}).Text).To(BeEmpty())
	})

	It("is cut at 4 KiB and marks the standing rules as the user's file", func() {
		rd.files["alice:LocalAI/agents/ag1/profile.md"] = strings.Repeat("rule. ", 2000)
		p := ld.Profile(context.Background(), contextfiles.Subject{Principal: "alice", AgentID: "ag1"})
		Expect(p.Text).To(ContainSubstring("Agent profile"))
		Expect(strings.Count(p.Text, "rule.")).To(BeNumerically("<=", contextfiles.MaxProfileBytes/6))
		Expect(p.Key).NotTo(BeEmpty())
	})
})

var _ = Describe("detail files", func() {
	var (
		ctx context.Context
		rd  *memReader
		ld  *contextfiles.Loader
		sub contextfiles.Subject
	)
	BeforeEach(func() {
		ctx = context.Background()
		rd = &memReader{files: map[string]string{
			"alice:Memory/home.md":                 "Rome since 2020.",
			"alice:Memory/agents/ag1/style.md":     "Short answers.",
			"alice:Memory/agents/ag2/style.md":     "ag2 private.",
			"alice:Memory/big.md":                  strings.Repeat("b", 20000),
			"alice:Memory/agents/ag1/home.md":      "agent-level home",
			"alice:Memory/channels/x/not-mine.md":  "other channel",
			"alice:Memory/secrets/outside-root.md": "outside",
		}}
		ld = contextfiles.NewLoader(contextfiles.NewRegistry(), rd, nil)
		reg := ld.Registry()
		Expect(reg.Register(contextfiles.Source{ID: "u", Scope: contextfiles.ScopeUser, Path: "Memory/index.md"})).To(Succeed())
		Expect(reg.Register(contextfiles.Source{ID: "a", Scope: contextfiles.ScopeAgent, Path: "Memory/agents/{agent}/index.md"})).To(Succeed())
		sub = contextfiles.Subject{Principal: "alice", AgentID: "ag1"}
	})

	It("reads a topic next to the index of a scope", func() {
		out, err := ld.Detail(ctx, sub, contextfiles.ScopeUser, "home")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Rome since 2020."))
	})

	It("accepts the file name the index line gives", func() {
		out, err := ld.Detail(ctx, sub, contextfiles.ScopeAgent, "style.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Short answers."))
	})

	It("without a scope looks in the narrowest scope first and names it", func() {
		out, err := ld.Detail(ctx, sub, "", "home")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("agent-level home"))
		Expect(out).To(ContainSubstring("(agent)"))
	})

	It("never reaches the file of another agent, whatever the model says", func() {
		_, err := ld.Detail(ctx, contextfiles.Subject{Principal: "alice"}, contextfiles.ScopeAgent, "style")
		Expect(err).To(MatchError(contextfiles.ErrNoTopic))
		for _, r := range rd.reads {
			Expect(r).NotTo(ContainSubstring("ag2"))
		}
	})

	DescribeTable("refuses a topic that is not a plain name",
		func(topic string) {
			_, err := ld.Detail(ctx, sub, "", topic)
			Expect(err).To(MatchError(contextfiles.ErrBadTopic))
			Expect(rd.reads).To(BeEmpty())
		},
		Entry("parent", "../secrets/outside-root"),
		Entry("slash", "agents/ag2/style"),
		Entry("absolute", "/etc/passwd"),
		Entry("dots", ".."),
		Entry("empty", "  "),
		Entry("backslash", `a\b`),
		Entry("too long", strings.Repeat("a", 100)),
		Entry("control", "a\x00b"),
	)

	It("reports a missing topic and a bad scope", func() {
		_, err := ld.Detail(ctx, sub, "", "nothing")
		Expect(err).To(MatchError(contextfiles.ErrNoTopic))
		_, err = ld.Detail(ctx, sub, "team", "home")
		Expect(err).To(MatchError(contextfiles.ErrBadTopic))
	})

	It("bounds what one call returns", func() {
		out, err := ld.Detail(ctx, sub, contextfiles.ScopeUser, "big")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(out)).To(BeNumerically("<=", contextfiles.MaxDetailBytes+200))
		Expect(out).To(ContainSubstring(contextfiles.TruncatedNote))
	})
})

var _ = Describe("the write limits", func() {
	src := contextfiles.Source{ID: "u", Scope: contextfiles.ScopeUser, Path: "Memory/index.md"}

	It("accepts an index inside the caps", func() {
		Expect(contextfiles.CheckIndex(src, []byte("- a: b\n"), []byte(lines(40)))).To(Succeed())
	})

	It("returns the current entries when a write goes over the lines or the bytes", func() {
		current := "- keep: this entry -> keep.md\n"
		for _, next := range []string{lines(41), strings.Repeat("x", contextfiles.MaxIndexBytes+1)} {
			err := contextfiles.CheckIndex(src, []byte(current), []byte(next))
			var over *contextfiles.OverCapError
			Expect(errors.As(err, &over)).To(BeTrue())
			Expect(over.Current).To(Equal(current))
			Expect(err.Error()).To(ContainSubstring("merge"))
			Expect(err.Error()).To(ContainSubstring("- keep: this entry"))
		}
	})

	It("checks the profile against 4 KiB", func() {
		Expect(contextfiles.CheckProfile([]byte("old"), []byte(strings.Repeat("p", contextfiles.MaxProfileBytes)))).To(Succeed())
		var over *contextfiles.OverCapError
		Expect(errors.As(contextfiles.CheckProfile([]byte("old"), []byte(strings.Repeat("p", contextfiles.MaxProfileBytes+1))), &over)).To(BeTrue())
		Expect(over.Current).To(Equal("old"))
	})
})

var _ = Describe("sources a turn brings with it", func() {
	var (
		ctx context.Context
		rd  *memReader
		ld  *contextfiles.Loader
	)
	user := contextfiles.Source{ID: "memory", Scope: contextfiles.ScopeUser, Path: "Memory/index.md"}
	agent := contextfiles.Source{ID: "memory", Scope: contextfiles.ScopeAgent, Path: "Memory/agents/{agent}/index.md"}
	channel := contextfiles.Source{ID: "memory", Scope: contextfiles.ScopeChannel, Path: "Memory/channels/{channel}/index.md"}
	BeforeEach(func() {
		ctx = context.Background()
		rd = &memReader{files: map[string]string{
			"alice:Memory/index.md":                    "- home: lives in Rome -> home.md\n",
			"alice:Memory/agents/ag1/index.md":         "- style: short\n",
			"alice:Memory/channels/room-1a2b/index.md": "- team: blue\n",
			"alice:Memory/home.md":                     "Rome since 2020.",
			"alice:Memory/channels/room-1a2b/team.md":  "Blue team.",
		}}
		ld = contextfiles.NewLoader(contextfiles.NewRegistry(), rd, nil)
	})

	It("loads them with no registered source and keeps the scope order", func() {
		sub := contextfiles.Subject{Principal: "alice", AgentID: "ag1", ChannelDir: "room-1a2b"}
		b := ld.Block(ctx, sub, channel, user, agent)
		u, a, c := strings.Index(b.Text, "lives in Rome"), strings.Index(b.Text, "short"), strings.Index(b.Text, "blue")
		Expect(u).To(BeNumerically(">", 0))
		Expect(a).To(BeNumerically(">", u))
		Expect(c).To(BeNumerically(">", a))
	})

	It("adds nothing when the turn brings none and none is registered", func() {
		Expect(ld.Block(ctx, contextfiles.Subject{Principal: "alice"}).Text).To(BeEmpty())
	})

	It("reads one file once when a registered source and a brought source name it", func() {
		Expect(ld.Registry().Register(contextfiles.Source{ID: "host", Scope: contextfiles.ScopeUser, Path: "Memory/index.md"})).To(Succeed())
		ld.Block(ctx, contextfiles.Subject{Principal: "alice"}, user)
		Expect(rd.reads).To(Equal([]string{"alice:Memory/index.md"}))
	})

	It("uses the channel folder the host names and never builds one from the model", func() {
		sub := contextfiles.Subject{Principal: "alice", ChannelDir: "room-1a2b"}
		Expect(ld.Block(ctx, sub, channel).Text).To(ContainSubstring("blue"))
		out, err := ld.Detail(ctx, sub, contextfiles.ScopeChannel, "team", channel)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Blue team."))
		for _, bad := range []string{"../x", "a/b", "", "a b", strings.Repeat("a", 90)} {
			Expect(ld.Block(ctx, contextfiles.Subject{Principal: "alice", ChannelDir: bad}, channel).Text).NotTo(ContainSubstring("channel notes"), bad)
		}
	})

	It("reads a topic of a brought source", func() {
		out, err := ld.Detail(ctx, contextfiles.Subject{Principal: "alice"}, "", "home", user)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Rome since 2020."))
		_, err = ld.Detail(ctx, contextfiles.Subject{Principal: "alice"}, "", "home")
		Expect(err).To(MatchError(contextfiles.ErrNoTopic))
	})

	It("names the save tool of the chat, and only when the chat has it", func() {
		sub := contextfiles.Subject{Principal: "alice"}
		Expect(ld.Block(ctx, sub, user).Text).NotTo(ContainSubstring("Save new facts"))
		sub.SaveTool = "propose_change"
		Expect(ld.Block(ctx, sub, user).Text).To(ContainSubstring("Save new facts with propose_change."))
	})

	It("exports the check a source passes before it is used", func() {
		Expect(user.Validate()).To(Succeed())
		Expect(contextfiles.Source{ID: "x", Scope: contextfiles.ScopeUser, Path: "../x.md"}.Validate()).To(MatchError(contextfiles.ErrInvalidSource))
	})
})
