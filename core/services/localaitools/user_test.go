package localaitools

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/http/auth"
)

// userSpy records the user each get_usage_stats call sees in its context.
type userSpy struct {
	*fakeClient
	seen []*auth.User
}

func (s *userSpy) GetUsageStats(ctx context.Context, q UsageStatsQuery) (*UsageStats, error) {
	s.mu.Lock()
	s.seen = append(s.seen, UserFromContext(ctx))
	s.mu.Unlock()
	return s.fakeClient.GetUsageStats(ctx, q)
}

var _ = Describe("acting user", func() {
	alice := &auth.User{ID: "alice"}
	bob := &auth.User{ID: "bob"}

	It("WithUser and UserFromContext round-trip, and a nil user adds nothing", func() {
		Expect(UserFromContext(context.Background())).To(BeNil())
		Expect(UserFromContext(WithUser(context.Background(), alice))).To(BeIdenticalTo(alice))
		Expect(UserFromContext(WithUser(context.Background(), nil))).To(BeNil())
	})

	It("carries the user through the MCP session to the handler, and never past the call", func() {
		spy := &userSpy{fakeClient: &fakeClient{}}
		ctx, sess, cleanup := connectInMemory(spy, Options{})
		DeferCleanup(cleanup)

		call := func(u *auth.User) {
			meta, release := ActingUserMeta(WithUser(ctx, u))
			defer release()
			_, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: ToolGetUsageStats, Meta: meta})
			Expect(err).ToNot(HaveOccurred())
		}
		call(alice)
		call(bob)
		call(nil)
		Expect(spy.seen).To(HaveLen(3))
		Expect(spy.seen[0]).To(BeIdenticalTo(alice))
		Expect(spy.seen[1]).To(BeIdenticalTo(bob))
		Expect(spy.seen[2]).To(BeNil())
	})

	It("ignores a token that is unknown or already released", func() {
		spy := &userSpy{fakeClient: &fakeClient{}}
		ctx, sess, cleanup := connectInMemory(spy, Options{})
		DeferCleanup(cleanup)

		meta, release := ActingUserMeta(WithUser(ctx, alice))
		release()
		_, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: ToolGetUsageStats, Meta: meta})
		Expect(err).ToNot(HaveOccurred())
		_, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: ToolGetUsageStats, Meta: map[string]any{userTokenMeta: "forged"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(spy.seen).To(Equal([]*auth.User{nil, nil}))
	})
})
