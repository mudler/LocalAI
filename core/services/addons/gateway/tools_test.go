// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

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
		return nil, errors.New("not installed")
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
		Expect(AuthorizeTool(ctx, toolSet{err: errors.New("manifest unavailable")}, "sample", 1, json.RawMessage(`{"tool":"summarize"}`))).To(MatchError(ErrForbidden), "manifest unavailable")
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
})
