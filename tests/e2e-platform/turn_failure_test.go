package e2eplatform_test

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A turn must end, with an outcome the user can see, when its model cannot be
// loaded. The model here names a backend that does not exist.
var _ = Describe("A turn whose model cannot load", Label("platform", "turns", "slow"), func() {
	It("ends as failed within two minutes instead of staying running", func() {
		in := startInstance(startOpts{Name: "brokenmodel", Models: map[string]string{
			"broken.yaml": "name: broken\nbackend: no-such-backend\nparameters:\n  model: broken.bin\n",
		}})
		chat := in.api.mustJSON(http.MethodPost, "/api/chats", obj{"model": "broken"}, http.StatusCreated)["id"].(string)
		in.api.mustJSON(http.MethodPost, "/api/chats/"+chat+"/messages", obj{"text": "hello"}, http.StatusOK)
		ended := func() string {
			c := in.api.mustJSON(http.MethodGet, "/api/chats/"+chat, nil, http.StatusOK)
			if running, _ := c["running"].(bool); running {
				return ""
			}
			last, _ := c["last_turn"].(string)
			return last
		}
		if ended() == "" {
			Eventually(ended, 120*time.Second, time.Second).ShouldNot(BeEmpty(), "the turn kept retrying a model that cannot load")
		}
		Expect(ended()).To(Equal("failed"))
	})
})
