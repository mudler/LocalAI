// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The chats routes and the feature registry", func() {
	It("holds one registry entry for each registered route, and no more", func() {
		registered := map[string]bool{}
		for _, entry := range auth.RouteFeatureRegistry {
			if entry.Feature == auth.FeatureChats {
				registered[entry.Method+" "+entry.Pattern] = true
			}
		}
		seen := map[string]bool{}
		for _, r := range newStudioServer().e.Routes() {
			if !strings.HasPrefix(r.Path, "/api/chats") {
				continue
			}
			switch r.Method {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				continue
			}
			key := r.Method + " " + r.Path
			seen[key] = true
			Expect(registered).To(HaveKey(key), "a chats route without a feature registry entry: it would have no gate")
		}
		for key := range registered {
			Expect(seen).To(HaveKey(key), "a registry entry without a registered route")
		}
	})
})
