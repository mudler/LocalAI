// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingLibraries stands in for the owner's library. It records the actor
// and the call of each library verb.
type recordingLibraries struct {
	actors []contract.Actor
	calls  []LibraryCall
}

func (r *recordingLibraries) Library(_ context.Context, a contract.Actor, c LibraryCall) (json.RawMessage, error) {
	r.actors = append(r.actors, a)
	r.calls = append(r.calls, c)
	return json.RawMessage(`{"official/whisper":[]}`), nil
}
func (r *recordingLibraries) Snapshot(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

var _ = Describe("the library system call", func() {
	releases := LibraryCall{Op: LibraryBackendReleases, Refs: []LibraryRef{{SourceID: "official", Name: "whisper"}}}

	It("runs the backend-releases check for a pinned frontend without an actor", func() {
		libs := &recordingLibraries{}
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Libraries: libs, Revalidate: func(context.Context, contract.Actor) error { return nil }})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		raw, e := cs["front-a"].LibrarySystem(context.Background(), releases)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring("official/whisper"))
		Expect(libs.calls).To(HaveLen(1))
		Expect(libs.calls[0].System).To(BeTrue())
		Expect(libs.actors[0]).To(Equal(contract.Actor{}))
	})

	It("refuses a system call from a peer that is not pinned", func() {
		libs := &recordingLibraries{}
		env := carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Libraries: libs, Revalidate: func(context.Context, contract.Actor) error { return nil }})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		stranger := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{env.certs["stranger"]}, RootCAs: env.roots, MinVersion: tls.VersionTLS13,
		}}}
		DeferCleanup(stranger.CloseIdleConnections)
		body, _ := json.Marshal(LibraryCall{Op: LibraryBackendReleases, System: true})
		resp, e := stranger.Post(env.server.URL+"/addons/library/system", "application/json", bytes.NewReader(body))
		Expect(e).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(libs.calls).To(BeEmpty())
	})

	It("never lets a system call change anything or ride the user-facing route", func() {
		libs := &recordingLibraries{}
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Libraries: libs, Revalidate: func(context.Context, contract.Actor) error { return nil }})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		_, e := cs["front-a"].LibrarySystem(context.Background(), LibraryCall{Op: LibraryRemove, ID: "x"})
		Expect(e).To(MatchError(ErrForbidden))
		Expect(libs.calls).To(BeEmpty())
		Expect(LibraryCall{Op: LibraryBackendReleases, System: true}.valid()).To(BeTrue())
		Expect(LibraryCall{Op: LibraryAdd, System: true}.valid()).To(BeFalse())
	})
})
