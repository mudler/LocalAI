// SPDX-License-Identifier: MIT
package application

import (
	"github.com/mudler/LocalAI/core/services/music/outputstore"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Music output dependency injection", func() {
	It("defaults to unavailable without constructing storage", func() {
		app := &Application{}
		Expect(app.MusicOutputStore()).To(BeNil())
	})
	It("accepts the real output store structurally without HTTP dependencies", func() {
		app := &Application{}
		store := &outputstore.Store{}
		app.SetMusicOutputStore(store)
		Expect(app.MusicOutputStore()).To(BeIdenticalTo(store))
		app.SetMusicOutputStore(nil)
		Expect(app.MusicOutputStore()).To(BeNil())
	})
})
