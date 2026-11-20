package studio

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

var _ = Describe("the conversation JSON of a chat with pulled projects", func() {
	It("returns the stored list: own ids and s:<node> references, nothing else", func() {
		rec := session.ChatRecord{ID: "c1", Principal: "alice-account", Title: "t", ProjectID: "own1", PulledProjects: []string{"own2", "s:42"}}
		b, err := json.Marshal(conversation(rec, store.TurnInfo{}))
		Expect(err).NotTo(HaveOccurred())
		var got struct {
			Pulled []string `json:"pulled_projects"`
		}
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		Expect(got.Pulled).To(Equal([]string{"own2", "s:42"}))
		Expect(string(b)).NotTo(ContainSubstring("alice-account"))
		Expect(string(b)).NotTo(ContainSubstring("/users/"))
	})
})
