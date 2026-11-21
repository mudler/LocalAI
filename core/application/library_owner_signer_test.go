// SPDX-License-Identifier: MIT
package application

import (
	"context"

	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingAdmin keeps the add request the owner passes on.
type recordingAdmin struct {
	library.Admin
	added library.AddRequest
}

func (r *recordingAdmin) Add(_ context.Context, req library.AddRequest) (library.Info, error) {
	r.added = req
	return library.Info{ID: req.ID}, nil
}

var _ = Describe("a library add forwarded to the owner", func() {
	It("carries the identity pattern and the source repository", func() {
		rec := &recordingAdmin{}
		o := &libraryOwner{admin: rec, adminOnly: func(context.Context, contract.Actor) error { return nil }}
		call := gateway.LibraryCall{Op: gateway.LibraryAdd, Kind: "addon", URL: "oci://registry.example/libs/fixture-addon:1", ID: "fixture",
			Issuer: "https://issuer.example", IdentityRegex: `^https://github\.com/example-org/shared/.+$`, SourceRepository: "https://github.com/example-org/example-repo"}
		_, err := o.Library(context.Background(), contract.Actor{UserID: "owner"}, call)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.added.IdentityRegex).To(Equal(call.IdentityRegex))
		Expect(rec.added.SourceRepository).To(Equal(call.SourceRepository))
		Expect(rec.added.Issuer).To(Equal(call.Issuer))
	})
})
