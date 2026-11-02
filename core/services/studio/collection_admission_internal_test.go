// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type targetedCollections struct {
	checked string
	denied  error
}

func (*targetedCollections) Allowed(context.Context, string) bool { return true }
func (*targetedCollections) List(context.Context, string) ([]string, error) {
	return nil, errors.New("unrelated legacy listing must not authorize destination")
}
func (*targetedCollections) Search(context.Context, string, string, string, int) ([]CollectionHit, error) {
	return nil, nil
}
func (c *targetedCollections) CheckCollection(_ context.Context, _ string, name string) error {
	c.checked = name
	return c.denied
}

var _ = Describe("targeted collection admission", func() {
	It("checks the requested destination without invoking global listing", func() {
		c := &targetedCollections{}
		s := &Service{d: Deps{Collections: c}}
		Expect(s.checkCollection(context.Background(), "principal", "fresh")).To(Succeed())
		Expect(c.checked).To(Equal("fresh"))
		failure := errors.New("explicit legacy resolution required")
		c.denied = failure
		Expect(s.checkCollection(context.Background(), "principal", "legacy")).To(MatchError(failure))
	})
})
