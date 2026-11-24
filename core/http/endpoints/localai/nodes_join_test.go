package localai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/natsauth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The join of a worker: a join token or the shared registration token proves
// who the worker is once, and the answer holds the own credential of its node.
var _ = Describe("Registration of a worker with a join token", func() {
	const regToken = "the-registration-token"

	var (
		ctx      context.Context
		registry *nodes.NodeRegistry
		joins    *cluster.JoinTokenStore
		limiter  *cluster.JoinFailureLimiter
		shared   string
		tunnels  *fakeTunnels
	)

	mint := func(req cluster.JoinTokenRequest) string {
		GinkgoHelper()
		token, _, err := joins.Mint(ctx, req)
		Expect(err).ToNot(HaveOccurred())
		return token
	}

	// register sends a registration. bearer is the own credential of a node.
	register := func(body, bearer string, autoApprove bool) (int, map[string]any, string) {
		GinkgoHelper()
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h := RegisterNodeEndpoint(registry, shared, autoApprove, nil, "", natsauth.Config{},
			WithJoinTokens(joins, limiter), WithWorkerToken("data-plane-token"), WithTunnelDisconnector(tunnels))
		Expect(h(e.NewContext(req, rec))).To(Succeed())
		var resp map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec.Code, resp, rec.Body.String()
	}

	BeforeEach(func() {
		ctx = context.Background()
		db := testutil.SetupTestDB()
		var err error
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		joins, err = cluster.NewJoinTokenStore(db)
		Expect(err).ToNot(HaveOccurred())
		limiter = cluster.NewJoinFailureLimiter(5, time.Minute)
		shared = ""
		tunnels = &fakeTunnels{}
	})

	It("registers the worker, pending, and hands over its own credential and the data plane token", func() {
		token := mint(cluster.JoinTokenRequest{})
		code, resp, body := register(`{"name":"w","address":"10.0.0.1:50051","join_token":"`+token+`","routes":["tunnel"]}`, "", false)
		Expect(code).To(Equal(http.StatusCreated), body)
		Expect(resp["status"]).To(Equal(nodes.StatusPending), "admin approval stays required")
		Expect(resp["node_token"]).ToNot(BeEmpty())
		Expect(resp["worker_token"]).To(Equal("data-plane-token"))
		Expect(resp).To(HaveKey("tunnel_token"))

		node, err := registry.GetByName(ctx, "w")
		Expect(err).ToNot(HaveOccurred())
		Expect(node.NodeTokenHash).To(Equal(nodes.HashNodeSecret(resp["node_token"].(string))))
		Expect(node.NodeTokenHash).ToNot(ContainSubstring(resp["node_token"].(string)), "only a hash is stored")
		Expect(node.JoinTokenID).ToNot(BeEmpty())
		Expect(node.TokenHash).To(BeEmpty(), "no shared token was involved")
	})

	It("never answers with the token it was given", func() {
		token := mint(cluster.JoinTokenRequest{})
		_, _, body := register(`{"name":"w","join_token":"`+token+`x"}`, "", false)
		Expect(body).ToNot(ContainSubstring(token))
		code, _, body := register(`{"name":"w","join_token":"`+token+`"}`, "", false)
		Expect(code).To(Equal(http.StatusCreated))
		Expect(body).ToNot(ContainSubstring(token))
	})

	It("approves at once only when the token says so", func() {
		token := mint(cluster.JoinTokenRequest{AutoApprove: true})
		code, resp, _ := register(`{"name":"w","join_token":"`+token+`"}`, "", false)
		Expect(code).To(Equal(http.StatusCreated))
		Expect(resp["status"]).To(Equal(nodes.StatusHealthy))
	})

	It("accepts a one-use token once", func() {
		token := mint(cluster.JoinTokenRequest{})
		code, _, _ := register(`{"name":"a","join_token":"`+token+`"}`, "", false)
		Expect(code).To(Equal(http.StatusCreated))
		code, _, _ = register(`{"name":"b","join_token":"`+token+`"}`, "", false)
		Expect(code).To(Equal(http.StatusUnauthorized))
		_, err := registry.GetByName(ctx, "b")
		Expect(err).To(HaveOccurred(), "no node is created by a token that is spent")
	})

	It("lets only one of two racing registrations win a one-use token", func() {
		token := mint(cluster.JoinTokenRequest{})
		var created, refused atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, name := range []string{"r1", "r2", "r3", "r4"} {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				<-start
				code, _, _ := register(`{"name":"`+name+`","join_token":"`+token+`"}`, "", false)
				if code == http.StatusCreated {
					created.Add(1)
				} else {
					refused.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		Expect(created.Load()).To(Equal(int32(1)))
		Expect(refused.Load()).To(Equal(int32(3)))
		list, err := registry.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(list).To(HaveLen(1))
	})

	It("refuses an expired, a revoked and an unknown token the same way", func() {
		expired := mint(cluster.JoinTokenRequest{TTL: time.Second})
		time.Sleep(1100 * time.Millisecond)
		revoked, row, _ := joins.Mint(ctx, cluster.JoinTokenRequest{})
		Expect(joins.Revoke(ctx, row.ID, "admin")).To(Succeed())
		for _, token := range []string{expired, revoked, "ljt_0000000000000000.nope", "nope"} {
			code, _, body := register(`{"name":"w","join_token":"`+token+`"}`, "", false)
			Expect(code).To(Equal(http.StatusUnauthorized), token)
			Expect(body).To(ContainSubstring("join token is not valid"))
		}
	})

	It("refuses a connection that the token does not allow, and keeps the use", func() {
		token := mint(cluster.JoinTokenRequest{Routes: []string{"websocket"}})
		code, _, _ := register(`{"name":"w","join_token":"`+token+`","routes":["nats"],"address":"10.0.0.1:1"}`, "", false)
		Expect(code).To(Equal(http.StatusForbidden))
		code, _, _ = register(`{"name":"w","join_token":"`+token+`","routes":["tunnel"]}`, "", false)
		Expect(code).To(Equal(http.StatusCreated))
	})

	It("keeps the use of a token when the request is bad", func() {
		token := mint(cluster.JoinTokenRequest{})
		code, _, _ := register(`{"join_token":"`+token+`"}`, "", false) // no name
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _, _ = register(`{"name":"w","join_token":"`+token+`"}`, "", false)
		Expect(code).To(Equal(http.StatusCreated))
	})

	It("throttles a caller that keeps sending tokens that are not valid", func() {
		for range 5 {
			code, _, _ := register(`{"name":"w","join_token":"ljt_0000000000000000.nope"}`, "", false)
			Expect(code).To(Equal(http.StatusUnauthorized))
		}
		code, _, _ := register(`{"name":"w","join_token":"ljt_0000000000000000.nope"}`, "", false)
		Expect(code).To(Equal(http.StatusTooManyRequests))
		// A valid token from that source waits too: the limit is on the source.
		code, _, _ = register(`{"name":"w","join_token":"`+mint(cluster.JoinTokenRequest{})+`"}`, "", false)
		Expect(code).To(Equal(http.StatusTooManyRequests))
	})

	Describe("registering again", func() {
		var token, credential string

		BeforeEach(func() {
			token = mint(cluster.JoinTokenRequest{})
			code, resp, _ := register(`{"name":"w","join_token":"`+token+`"}`, "", true)
			Expect(code).To(Equal(http.StatusCreated))
			credential = resp["node_token"].(string)
		})

		It("takes the own credential and needs no token, and mints no new one", func() {
			code, resp, _ := register(`{"name":"w"}`, credential, true)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp).ToNot(HaveKey("node_token"))
			Expect(resp["worker_token"]).To(Equal("data-plane-token"))
			node, _ := registry.GetByName(ctx, "w")
			Expect(nodes.NodeCredentialMatches(node, credential)).To(BeTrue())
		})

		It("refuses a wrong credential with no token, also when the frontend is open", func() {
			code, _, _ := register(`{"name":"w"}`, "not-the-credential", true)
			Expect(code).To(Equal(http.StatusConflict), "no proof must not replace the credential of a node")
		})

		It("replaces the credential when a new join token registers the node, and ends its tunnel", func() {
			second := mint(cluster.JoinTokenRequest{})
			code, resp, _ := register(`{"name":"w","join_token":"`+second+`"}`, "", true)
			Expect(code).To(Equal(http.StatusCreated))
			fresh := resp["node_token"].(string)
			Expect(fresh).ToNot(Equal(credential))
			node, _ := registry.GetByName(ctx, "w")
			Expect(nodes.NodeCredentialMatches(node, credential)).To(BeFalse(), "the old credential stops working")
			Expect(nodes.NodeCredentialMatches(node, fresh)).To(BeTrue())
			Expect(tunnels.ended).To(ContainElement(node.ID))
		})

		It("keeps a node that joined over a websocket to the connections of its token", func() {
			only := mint(cluster.JoinTokenRequest{Routes: []string{"websocket"}})
			code, resp, _ := register(`{"name":"ws","join_token":"`+only+`"}`, "", true)
			Expect(code).To(Equal(http.StatusCreated))
			own := resp["node_token"].(string)
			code, _, _ = register(`{"name":"ws","routes":["nats"],"address":"10.0.0.1:1"}`, own, true)
			Expect(code).To(Equal(http.StatusForbidden))
			code, _, _ = register(`{"name":"ws","routes":["tunnel"]}`, own, true)
			Expect(code).To(Equal(http.StatusCreated))
		})
	})

	Describe("with the shared registration token", func() {
		BeforeEach(func() { shared = regToken })

		It("still registers a worker, and gives it its own credential", func() {
			code, resp, _ := register(`{"name":"s","token":"`+regToken+`"}`, "", false)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp["node_token"]).ToNot(BeEmpty())
			node, _ := registry.GetByName(ctx, "s")
			Expect(node.TokenHash).To(Equal(nodes.HashNodeSecret(regToken)))
			Expect(node.JoinTokenID).To(BeEmpty())
		})

		It("refuses a registration with neither the token nor a join token", func() {
			code, _, _ := register(`{"name":"s"}`, "", false)
			Expect(code).To(Equal(http.StatusUnauthorized))
			code, _, _ = register(`{"name":"s","token":"wrong"}`, "", false)
			Expect(code).To(Equal(http.StatusUnauthorized))
		})

		It("needs no shared token for a worker that has a join token", func() {
			code, _, _ := register(`{"name":"j","join_token":"`+mint(cluster.JoinTokenRequest{})+`"}`, "", false)
			Expect(code).To(Equal(http.StatusCreated))
		})

		It("does not fall back to the shared token when a join token is wrong", func() {
			code, _, _ := register(`{"name":"j","token":"`+regToken+`","join_token":"ljt_0000000000000000.nope"}`, "", false)
			Expect(code).To(Equal(http.StatusUnauthorized))
		})

		It("replaces the credential of a node when the shared token registers it again", func() {
			_, first, _ := register(`{"name":"s","token":"`+regToken+`"}`, "", false)
			_, second, _ := register(`{"name":"s","token":"`+regToken+`"}`, "", false)
			Expect(second["node_token"]).ToNot(Equal(first["node_token"]))
			node, _ := registry.GetByName(ctx, "s")
			Expect(nodes.NodeCredentialMatches(node, first["node_token"].(string))).To(BeFalse())
		})
	})
})

type fakeTunnels struct {
	mu    sync.Mutex
	ended []string
}

func (f *fakeTunnels) Disconnect(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, id)
	return true
}
