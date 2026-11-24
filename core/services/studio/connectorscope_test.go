package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The authority of the host is the only gate between an add-on call and the
// token of a binding. These specs pin which operations a binding scope may run.
var _ = Describe("the authorization of a connector call", func() {
	ctx := context.Background()
	var (
		r *connRig
		b connectors.Binding
	)
	BeforeEach(func() {
		r = newConnRig(newShared(), connStore(), newScriptedBot(), connSay("ok"), nil)
		b = r.bind(studio.ConnectorInput{Allow: []string{"11"}})
	})
	history := func() contract.ConnectorRequest {
		return contract.ConnectorRequest{Op: contract.ConnectorOpHistory, Kind: b.Kind, Chat: "C1", Thread: "T1", Limit: 20, Before: "m5"}
	}

	It("runs the history of a thread for the binding, with the stored token and the request untouched", func() {
		got, err := r.conns.AuthorizeConnector(ctx, studio.BindingConnectorScope(b), history())
		Expect(err).NotTo(HaveOccurred())
		want := history()
		want.Token = botToken
		Expect(got).To(Equal(want))
	})

	It("ignores a token that the caller puts in a history request", func() {
		req := history()
		req.Token = "not-the-token"
		got, err := r.conns.AuthorizeConnector(ctx, studio.BindingConnectorScope(b), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Token).To(Equal(botToken))
	})

	It("refuses the history for a scope that does not match the stored binding", func() {
		for name, mutate := range map[string]func(*studio.ConnectorCallScope){
			"owner":   func(s *studio.ConnectorCallScope) { s.Principal = "mallory" },
			"add-on":  func(s *studio.ConnectorCallScope) { s.AddonID = "other" },
			"agent":   func(s *studio.ConnectorCallScope) { s.AgentID = "other" },
			"binding": func(s *studio.ConnectorCallScope) { s.BindingID = "missing" },
			"none":    func(s *studio.ConnectorCallScope) { s.BindingID = "" },
		} {
			s := studio.BindingConnectorScope(b)
			mutate(&s)
			_, err := r.conns.AuthorizeConnector(ctx, s, history())
			Expect(err).To(MatchError(connectors.ErrForbidden), name)
		}
		req := history()
		req.Kind = "other"
		_, err := r.conns.AuthorizeConnector(ctx, studio.BindingConnectorScope(b), req)
		Expect(err).To(MatchError(connectors.ErrForbidden), "a request of another kind")
		s := studio.BindingConnectorScope(b)
		s.Purpose = studio.ConnectorPurposeVerifyToken
		_, err = r.conns.AuthorizeConnector(ctx, s, history())
		Expect(err).To(MatchError(connectors.ErrForbidden), "a token check runs only the op me")
	})

	It("treats a disabled binding as it treats a send: only the poll stops", func() {
		_, err := r.conns.SetConnectorEnabled(ctx, alice, b.ID, false)
		Expect(err).NotTo(HaveOccurred())
		off, err := r.store.GetBinding(ctx, b.ID)
		Expect(err).NotTo(HaveOccurred())
		s := studio.BindingConnectorScope(off)
		send := contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: off.Kind, Chat: "C1", Text: "hi"}
		_, sendErr := r.conns.AuthorizeConnector(ctx, s, send)
		_, histErr := r.conns.AuthorizeConnector(ctx, s, history())
		Expect(sendErr).NotTo(HaveOccurred())
		Expect(histErr).NotTo(HaveOccurred())
		_, pollErr := r.conns.AuthorizeConnector(ctx, s, contract.ConnectorRequest{Op: contract.ConnectorOpPoll, Kind: off.Kind})
		Expect(pollErr).To(MatchError(connectors.ErrForbidden))
	})

	It("allows every operation of the contract for an enabled binding and refuses an unknown one", func() {
		s := studio.BindingConnectorScope(b)
		for _, op := range []string{
			contract.ConnectorOpPoll, contract.ConnectorOpSend, contract.ConnectorOpEdit, contract.ConnectorOpTyping, contract.ConnectorOpMe,
			contract.ConnectorOpMedia, contract.ConnectorOpSendFile, contract.ConnectorOpDraft, contract.ConnectorOpHistory, contract.ConnectorOpDelete,
		} {
			_, err := r.conns.AuthorizeConnector(ctx, s, contract.ConnectorRequest{Op: op, Kind: b.Kind})
			Expect(err).NotTo(HaveOccurred(), op)
		}
		_, err := r.conns.AuthorizeConnector(ctx, s, contract.ConnectorRequest{Op: "export", Kind: b.Kind})
		Expect(err).To(MatchError(connectors.ErrForbidden))
	})
})
