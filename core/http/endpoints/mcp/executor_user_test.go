package mcp_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	mcptools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/localaitools/inproc"
	"github.com/mudler/LocalAI/core/services/routing/billing"
)

// The assistant executor runs a tool as the user it was built for, so the
// in-process client answers for that user and not for the server default.
var _ = Describe("AsUser executor", func() {
	It("runs the assistant tools as the given user", func() {
		rec := billing.NewRecorder(billing.NewMemoryBackend(10))
		for _, id := range []string{"ann", "ben"} {
			Expect(rec.Record(context.Background(), &auth.UsageRecord{UserID: id, Source: auth.UsageSourceWeb, Model: "m", Endpoint: "/e", CreatedAt: time.Now()})).To(Succeed())
		}
		client := &inproc.Client{StatsRecorder: rec, FallbackUser: &auth.User{ID: "server"}, SingleUser: true}

		holder := mcptools.NewLocalAIAssistantHolder()
		ctx := context.Background()
		Expect(holder.Initialize(ctx, client, localaitools.Options{})).To(Succeed())
		DeferCleanup(holder.Close)

		viewer := func(exec mcptools.ToolExecutor) string {
			out, err := exec.ExecuteTool(ctx, localaitools.ToolGetUsageStats, `{}`)
			Expect(err).ToNot(HaveOccurred())
			return out
		}
		base := holder.Executor()
		Expect(viewer(base)).To(MatchRegexp(`"id":\s*"server"`))
		Expect(viewer(mcptools.AsUser(base, &auth.User{ID: "ann"}))).To(MatchRegexp(`"id":\s*"ann"`))
		Expect(viewer(mcptools.AsUser(base, &auth.User{ID: "ben"}))).To(MatchRegexp(`"id":\s*"ben"`))
		Expect(viewer(base)).To(MatchRegexp(`"id":\s*"server"`))
		Expect(mcptools.AsUser(base, nil)).To(BeIdenticalTo(base))

		// With authentication on (not single-user), a call without a user has
		// no identity: it is refused, it never becomes the server user.
		client.SingleUser = false
		Expect(viewer(base)).To(ContainSubstring("no user context"))
		Expect(viewer(mcptools.AsUser(base, &auth.User{ID: "ann"}))).To(MatchRegexp(`"id":\s*"ann"`))
	})
})
