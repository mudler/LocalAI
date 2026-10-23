// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// toolSet is a tool source keyed by add-on, with the generation it answers for.
type toolSet struct {
	gen   uint64
	tools map[string][]string
	err   error
}

func (t toolSet) DeclaredTools(_ context.Context, id string, gen uint64) ([]string, error) {
	if t.err != nil {
		return nil, t.err
	}
	list, ok := t.tools[id]
	if !ok || gen != t.gen {
		return nil, ErrForbidden
	}
	return list, nil
}

// countingClient counts every call that reaches the add-on.
type countingClient struct {
	contract.Runtime
	reads atomic.Int32
}

func (c *countingClient) Read(context.Context, contract.Envelope) (contract.Result, error) {
	c.reads.Add(1)
	return contract.Result{Payload: []byte(`{"ok":true}`)}, nil
}

var _ = Describe("declared tool authorization", Label("tools"), func() {
	ctx := context.Background()
	source := toolSet{gen: 1, tools: map[string][]string{"sample": {"summarize", "Ünï"}, "other": {"export"}}}

	It("allows only an exact declared tool name and fails closed otherwise", func() {
		allowed := []string{`{}`, `{"rows":[1]}`, `{"tool":"summarize","context":{}}`, `{"tool":"Ünï"}`}
		for _, a := range allowed {
			Expect(AuthorizeTool(ctx, source, "sample", 1, json.RawMessage(a))).To(Succeed(), a)
		}
		denied := map[string]string{
			"undeclared":          `{"tool":"delete-everything"}`,
			"another add-on tool": `{"tool":"export"}`,
			"case variant":        `{"tool":"Summarize"}`,
			"upper case":          `{"tool":"SUMMARIZE"}`,
			"trailing space":      `{"tool":"summarize "}`,
			"leading space":       `{"tool":" summarize"}`,
			"tab":                 `{"tool":"summarize\t"}`,
			"newline":             `{"tool":"summarize\n"}`,
			"unicode confusable":  `{"tool":"summarіze"}`,
			"decomposed unicode":  `{"tool":"Ünï"}`,
			"zero width":          `{"tool":"sum​marize"}`,
			"empty":               `{"tool":""}`,
			"null":                `{"tool":null}`,
			"number":              `{"tool":1}`,
			"array":               `{"tool":["summarize"]}`,
			"object":              `{"tool":{"name":"summarize"}}`,
			"key case variant":    `{"Tool":"summarize"}`,
			"key upper case":      `{"TOOL":"undeclared"}`,
			"key with space":      `{" tool":"summarize"}`,
			"declared plus alias": `{"tool":"summarize","Tool":"anything"}`,
		}
		for name, a := range denied {
			Expect(AuthorizeTool(ctx, source, "sample", 1, json.RawMessage(a))).To(MatchError(ErrForbidden), name)
		}
		Expect(AuthorizeTool(ctx, source, "sample", 2, json.RawMessage(`{"tool":"summarize"}`))).To(MatchError(ErrForbidden), "another generation")
		Expect(AuthorizeTool(ctx, source, "ghost", 1, json.RawMessage(`{"tool":"summarize"}`))).To(MatchError(ErrForbidden), "not installed")
		Expect(AuthorizeTool(ctx, nil, "sample", 1, json.RawMessage(`{"tool":"summarize"}`))).To(MatchError(ErrForbidden), "no source")
		Expect(AuthorizeTool(ctx, source, "sample", 1, json.RawMessage(`not json`))).To(MatchError(ErrForbidden), "not an object")
	})

	It("never contacts the add-on for an undeclared tool and still serves a declared one", func() {
		admission := &admitted{}
		client := &countingClient{}
		g := &Gateway{Admission: admission, Resolver: resolved{}, Runtime: currentFixture{client}, Tools: source, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		read := func(args string) error {
			_, e := g.Dispatch(ctx, contract.Actor{}, contract.Request{AddonID: "sample", ResourceID: "x", Generation: 1, OperationID: "op", Action: "read", Arguments: json.RawMessage(args)})
			return e
		}
		Expect(read(`{"tool":"summarize","context":{"conversationId":"c"}}`)).To(Succeed())
		Expect(client.reads.Load()).To(Equal(int32(1)))
		for _, a := range []string{`{"tool":"anything"}`, `{"tool":"export"}`, `{"tool":"SUMMARIZE"}`, `{"Tool":"summarize"}`, `{"tool":"summarize "}`} {
			Expect(read(a)).To(MatchError(ErrForbidden), a)
		}
		Expect(client.reads.Load()).To(Equal(int32(1)), "the runtime is not contacted on a denial")
		// Without any tool source a tool request is denied, not trusted.
		g.Tools = nil
		Expect(read(`{"tool":"summarize"}`)).To(MatchError(ErrForbidden))
		Expect(client.reads.Load()).To(Equal(int32(1)))
	})

	It("denies a repeated key at any depth however it is spelled", func() {
		repeated := map[string]string{
			"same tool key twice":        `{"tool":"summarize","tool":"export"}`,
			"declared first":             `{"tool":"summarize","tool":"summarize"}`,
			"undeclared then declared":   `{"tool":"wipe","tool":"summarize"}`,
			"escaped duplicate":          `{"tool":"summarize","\u0074ool":"wipe"}`,
			"escaped both":               `{"\u0074ool":"summarize","t\u006fol":"wipe"}`,
			"case folded duplicate":      `{"tool":"summarize","TOOL":"summarize"}`,
			"kelvin sign":                `{"tool":"summarize","\u212a":1,"k":2}`,
			"trimmed duplicate":          `{"tool":"summarize","tool ":"summarize"}`,
			"nested duplicate":           `{"tool":"summarize","context":{"a":1,"a":2}}`,
			"duplicate inside array":     `{"tool":"summarize","rows":[{"a":1,"a":1}]}`,
			"nested case folded":         `{"tool":"summarize","context":{"a":1,"A":2}}`,
			"unrelated top level repeat": `{"tool":"summarize","rows":1,"rows":2}`,
			"duplicate without tool":     `{"rows":1,"rows":2}`,
		}
		for name, a := range repeated {
			Expect(AuthorizeTool(ctx, source, "sample", 1, json.RawMessage(a))).To(MatchError(ErrForbidden), name)
		}
	})

	It("denies arguments that are not exactly one object", func() {
		for name, a := range map[string]string{
			"array":          `[{"tool":"summarize"}]`,
			"null":           `null`,
			"string":         `"summarize"`,
			"number":         `1`,
			"trailing value": `{"tool":"summarize"} {"tool":"wipe"}`,
			"trailing text":  `{"tool":"summarize"}x`,
			"truncated":      `{"tool":"summarize"`,
			"too deep":       `{"tool":"summarize","a":` + strings.Repeat(`[`, 80) + strings.Repeat(`]`, 80) + `}`,
		} {
			Expect(AuthorizeTool(ctx, source, "sample", 1, json.RawMessage(a))).To(MatchError(ErrForbidden), name)
		}
		Expect(AuthorizeTool(ctx, source, "sample", 1, json.RawMessage(` {"tool":"summarize"} `))).To(Succeed(), "white space around one object")
	})

	It("separates a denial from an unavailable declaration source", func() {
		call := `{"tool":"summarize"}`
		check := func(src ToolSource) error {
			return AuthorizeTool(ctx, src, "sample", 1, json.RawMessage(call))
		}
		for name, cause := range map[string]error{
			"not declared":  ErrForbidden,
			"stale":         addons.ErrStaleGeneration,
			"addons denial": addons.ErrForbidden,
		} {
			err := check(toolSet{err: cause})
			Expect(err).To(MatchError(ErrForbidden), name)
			Expect(StatusFor(err)).To(Equal(http.StatusForbidden), name)
		}
		for name, cause := range map[string]error{
			"closed manager":    addons.ErrNotOwner,
			"recovery required": addons.ErrRecoveryRequired,
			"owner busy":        addons.ErrOwnerBusy,
			"cancelled":         context.Canceled,
			"deadline":          context.DeadlineExceeded,
			"unclassified":      errors.New("manifest unavailable"),
			"denial and outage": fmt.Errorf("%w: %w", ErrForbidden, addons.ErrNotOwner),
		} {
			err := check(toolSet{err: cause})
			Expect(err).To(HaveOccurred(), name)
			Expect(errors.Is(err, ErrUnavailable)).To(BeTrue(), name)
			Expect(errors.Is(err, cause)).To(BeTrue(), name)
			Expect(StatusFor(err)).To(Equal(http.StatusServiceUnavailable), name)
		}
	})

	It("answers 503 and never contacts the add-on when the source is unavailable or the arguments repeat a key", func() {
		client := &countingClient{}
		src := &flipTools{toolSet: source}
		g := &Gateway{Admission: &admitted{}, Resolver: resolved{}, Runtime: currentFixture{client}, Tools: src, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		read := func(args string) error {
			_, e := g.Dispatch(ctx, contract.Actor{}, contract.Request{AddonID: "sample", ResourceID: "x", Generation: 1, OperationID: "op", Action: "read", Arguments: json.RawMessage(args)})
			return e
		}
		Expect(read(`{"tool":"summarize"}`)).To(Succeed())
		for _, a := range []string{`{"tool":"wipe","tool":"summarize"}`, `{"tool":"summarize","tool":"wipe"}`, `{"tool":"summarize"} {}`} {
			Expect(read(a)).To(MatchError(ErrForbidden), a)
		}
		for _, cause := range []error{addons.ErrNotOwner, addons.ErrRecoveryRequired, context.Canceled} {
			src.set(cause)
			err := read(`{"tool":"summarize"}`)
			Expect(StatusFor(err)).To(Equal(http.StatusServiceUnavailable), cause.Error())
			Expect(err).NotTo(MatchError(ErrForbidden))
		}
		src.set(nil)
		Expect(client.reads.Load()).To(Equal(int32(1)), "the runtime is contacted only for the one valid request")
	})
})

// flipTools is a tool source whose failure the test changes.
type flipTools struct {
	toolSet
	mu  sync.Mutex
	err error
}

func (f *flipTools) set(e error) { f.mu.Lock(); f.err = e; f.mu.Unlock() }
func (f *flipTools) DeclaredTools(ctx context.Context, id string, gen uint64) ([]string, error) {
	f.mu.Lock()
	e := f.err
	f.mu.Unlock()
	if e != nil {
		return nil, e
	}
	return f.toolSet.DeclaredTools(ctx, id, gen)
}
