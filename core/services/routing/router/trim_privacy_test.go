package router

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/privacy"
)

var _ = Describe("conversation trim and the privacy level", func() {
	It("tokenizes the turns of a request on the context of that request", func() {
		var seen []privacy.Level
		tok := Tokenizer(func(ctx context.Context, s string) (int, error) {
			seen = append(seen, privacy.FromContext(ctx))
			return len(strings.Fields(s)), nil
		})
		b := &lazyBudget{tokenize: tok, maxContext: 1000}
		ctx := privacy.WithLevel(context.Background(), privacy.ZeroRetention)
		p := Probe{Prompt: "a b c", Messages: []string{"first turn", "second turn"}}
		Expect(trimmedProbeText(ctx, p, b, identityRender)).To(ContainSubstring("second turn"))
		Expect(seen).To(ContainElement(privacy.ZeroRetention), "the turns are counted with the level of the request")
	})
})
