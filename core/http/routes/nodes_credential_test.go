package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/natsauth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The node self-service routes take the own credential of a node, for that node
// only. The registration token opens registration and nothing else.
var _ = Describe("Node self-service credentials", func() {
	const regToken = "shared-registration-token"

	var (
		e        *echo.Echo
		registry *nodes.NodeRegistry
	)

	call := func(method, path, body, bearer string) *httptest.ResponseRecorder {
		GinkgoHelper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	// join registers a worker with the shared token and returns its id and credential.
	join := func(name string) (string, string) {
		GinkgoHelper()
		rec := call(http.MethodPost, "/api/node/register", `{"name":"`+name+`","address":"10.0.0.1:50051","token":"`+regToken+`"}`, "")
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		node, err := registry.GetByName(GinkgoT().Context(), name)
		Expect(err).ToNot(HaveOccurred())
		return node.ID, extractNodeToken(rec.Body.String())
	}

	BeforeEach(func() {
		var err error
		registry, err = nodes.NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
		e = echo.New()
		RegisterNodeSelfServiceRoutes(e, registry, regToken, true, nil, "", natsauth.Config{})
	})

	routesOf := func(id string) [][3]string {
		return [][3]string{
			{http.MethodPost, "/api/node/" + id + "/heartbeat", `{}`},
			{http.MethodPost, "/api/node/" + id + "/drain", ""},
			{http.MethodPost, "/api/node/" + id + "/resume", ""},
			{http.MethodGet, "/api/node/" + id + "/models", ""},
			{http.MethodPost, "/api/node/" + id + "/deregister", ""},
		}
	}

	It("no longer accepts the shared registration token for a node route", func() {
		id, _ := join("w1")
		for _, r := range routesOf(id) {
			rec := call(r[0], r[1], r[2], regToken)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized), r[0]+" "+r[1])
		}
	})

	It("accepts the own credential of the node", func() {
		id, cred := join("w1")
		Expect(cred).ToNot(BeEmpty())
		for _, r := range routesOf(id) {
			rec := call(r[0], r[1], r[2], cred)
			Expect(rec.Code).To(Equal(http.StatusOK), r[0]+" "+r[1]+" "+rec.Body.String())
		}
	})

	It("refuses the credential of one node on another node", func() {
		_, credA := join("a")
		idB, _ := join("b")
		for _, r := range routesOf(idB) {
			rec := call(r[0], r[1], r[2], credA)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized), r[0]+" "+r[1])
		}
	})

	It("refuses no credential, a wrong one and an unknown node alike", func() {
		id, cred := join("w1")
		Expect(call(http.MethodPost, "/api/node/"+id+"/heartbeat", `{}`, "").Code).To(Equal(http.StatusUnauthorized))
		Expect(call(http.MethodPost, "/api/node/"+id+"/heartbeat", `{}`, "wrong").Code).To(Equal(http.StatusUnauthorized))
		Expect(call(http.MethodPost, "/api/node/missing/heartbeat", `{}`, cred).Code).To(Equal(http.StatusUnauthorized))
		Expect(call(http.MethodDelete, "/api/node/"+id, "", regToken).Code).To(Equal(http.StatusUnauthorized))
		Expect(registry.Get(GinkgoT().Context(), id)).ToNot(BeNil(), "the node is still there")
	})

	It("refuses a node that registered before credentials existed, until it registers again", func() {
		id, _ := join("old")
		Expect(registry.SetNodeCredential(GinkgoT().Context(), id, "", "", "")).To(Succeed())
		Expect(call(http.MethodPost, "/api/node/"+id+"/heartbeat", `{}`, regToken).Code).To(Equal(http.StatusUnauthorized))
	})

	It("stops the old credential as soon as the node registers again with the shared token", func() {
		id, old := join("w1")
		_, fresh := join("w1")
		Expect(call(http.MethodPost, "/api/node/"+id+"/heartbeat", `{}`, old).Code).To(Equal(http.StatusUnauthorized))
		Expect(call(http.MethodPost, "/api/node/"+id+"/heartbeat", `{}`, fresh).Code).To(Equal(http.StatusOK))
	})
})

// extractNodeToken reads node_token from the body of a registration answer.
func extractNodeToken(body string) string {
	const key = `"node_token":"`
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	rest := body[i+len(key):]
	return rest[:strings.Index(rest, `"`)]
}
