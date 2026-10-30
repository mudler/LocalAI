// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/provider"
)

var _ = Describe("Chats list and images", func() {
	It("names the model that describes images for a chat whose model cannot see them", func() {
		s := newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			d.Media = media.New(provider.New(), nil, func(media.Capability) string { return "" }).
				WithVisionChooser(func(chatModel string) string {
					if chatModel == "" {
						return "helper-vl"
					}
					return chatModel
				})
		})
		rec := s.do("alice", http.MethodGet, "/api/chats", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("image_model", "helper-vl"))
	})

	It("leaves the image model out when no model can describe images", func() {
		s := newStudioServer()
		rec := s.do("alice", http.MethodGet, "/api/chats", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(rec)).NotTo(HaveKey("image_model"))
	})
})
