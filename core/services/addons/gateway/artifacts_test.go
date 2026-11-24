// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("private artifact lookups", func() {
	ok := func(context.Context, contract.Actor) error { return nil }
	owner := func(g *Gateway) *fixtureEnv {
		return carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
	}
	It("returns the artifact status through the owner from every frontend with one denial for the rest", func() {
		files := dataArtifacts("a,b\n")
		env := owner(&Gateway{Revalidate: ok, Artifacts: files})
		local := &Gateway{Revalidate: ok, Artifacts: files}
		want, e := local.ArtifactStatus(context.Background(), frontActor, fakeArtifactID)
		Expect(e).NotTo(HaveOccurred())
		Expect(want.ID).To(Equal(fakeArtifactID))
		for _, front := range []string{"front-a", "front-b"} {
			g := &Gateway{Forward: env.carriers[front], Revalidate: ok}
			got, e := g.ArtifactStatus(context.Background(), frontActor, fakeArtifactID)
			Expect(e).NotTo(HaveOccurred(), front)
			Expect(got).To(Equal(want), front)
			_, e = g.ArtifactStatus(context.Background(), frontActor, "a"+strings.Repeat("9", 32))
			Expect(e).To(Equal(ErrForbidden), front)
		}
		// A frontend never answers from itself: without an owner there is no answer.
		_, e = (&Gateway{Revalidate: ok}).ArtifactStatus(context.Background(), frontActor, fakeArtifactID)
		Expect(e).To(Equal(ErrForbidden))
	})
	It("types the owner's refusal and outage of a lookup the same from every frontend", func() {
		for name, tc := range map[string]struct {
			files *fakeArtifacts
			want  error
		}{
			"denied":       {&fakeArtifacts{err: ErrForbidden}, ErrForbidden},
			"unavailable":  {&fakeArtifacts{err: ErrUnavailable}, ErrUnavailable},
			"unclassified": {&fakeArtifacts{err: context.DeadlineExceeded}, ErrUnavailable},
		} {
			env := owner(&Gateway{Revalidate: ok, Artifacts: tc.files})
			for _, front := range []string{"front-a", "front-b"} {
				_, e := (&Gateway{Forward: env.carriers[front], Revalidate: ok}).ArtifactStatus(context.Background(), frontActor, fakeArtifactID)
				Expect(e).To(Equal(tc.want), name+" via "+front)
			}
		}
		// The owner checks the account again before it asks the store.
		files := dataArtifacts("x")
		env := owner(&Gateway{Revalidate: func(context.Context, contract.Actor) error { return ErrForbidden }, Artifacts: files})
		_, e := (&Gateway{Forward: env.carriers["front-a"], Revalidate: ok}).ArtifactStatus(context.Background(), frontActor, fakeArtifactID)
		Expect(e).To(Equal(ErrForbidden))
		Expect(files.serves.Load()).To(BeZero())
	})
	It("writes an attachment header that cannot be broken out of", func() {
		for _, name := range []string{"report.csv", "a\"; inline; x=y", "line\r\nSet-Cookie: a=b", "résumé.csv", "100%.csv", "a/b\\c"} {
			h := ContentDisposition(name)
			Expect(h).To(HavePrefix(`attachment; filename="`), name)
			Expect(h).NotTo(ContainSubstring("\r"), name)
			Expect(h).NotTo(ContainSubstring("\n"), name)
			quoted := strings.SplitN(h, `; filename*=`, 2)[0]
			Expect(strings.Count(quoted, `"`)).To(Equal(2), name)
			Expect(quoted).NotTo(ContainSubstring(";"+" inline"), name)
		}
		Expect(ContentDisposition("résumé.csv")).To(Equal(`attachment; filename="r_sum_.csv"; filename*=UTF-8''r%C3%A9sum%C3%A9.csv`))
	})
})
