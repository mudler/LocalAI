package application

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("backgroundContext", func() {
	It("is the context of the configuration", func() {
		type key struct{}
		ctx := context.WithValue(context.Background(), key{}, "v")
		a := &Application{applicationConfig: &config.ApplicationConfig{Context: ctx}}
		Expect(a.backgroundContext().Value(key{})).To(Equal("v"))
	})

	It("falls back to Background when the configuration has none", func() {
		a := &Application{applicationConfig: &config.ApplicationConfig{}}
		Expect(a.backgroundContext()).To(Equal(context.Background()))
	})
})
