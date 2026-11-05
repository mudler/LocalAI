// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// What the host records about a tool call that used a connection: the kind,
// the connection, the resource and how it ended. The add-on here is a stand-in
// with a credential; nothing depends on a product.
var _ = Describe("the outcome of an add-on tool call", func() {
	const (
		secretBody = "SECRET-BODY-TEXT-do-not-log"
		write      = "addon__tracker__comment-item"
		read       = "addon__tracker__list-items"
	)
	var (
		f    *fake
		base *applyFake
		a    *tools.ToolAdapter
		ctx  context.Context
		out  *tools.Outcome
	)
	refused := func(upstream int, hint string) error {
		return gateway.Refused(&addonruntime.Refusal{Status: 400, Reason: "the token has no permission for this", Upstream: upstream, Hint: hint})
	}
	BeforeEach(func() {
		f = &fake{list: apps.List{Apps: []apps.App{trackerApp()}}}
		f.read = contract.Result{Payload: []byte(`{"files":[]}`)}
		f.proposal = gateway.Proposal{AddonID: "tracker", ResourceID: "r-0123456789", Action: "write", Digest: "d", State: gateway.ProposalPending}
		base = &applyFake{actFake: &actFake{fake: f}, state: gateway.ProposalApplied}
		a = tools.NewChatToolAdapter(base)
		ctx, out = tools.WithOutcome(tools.WithConnection(context.Background(), "b-work"))
	})
	call := func(name string) (tools.ToolResult, error) {
		in := `{"repo":"acme/widgets"}`
		if name == write {
			in = `{"repo":"acme/widgets","body":"` + secretBody + `"}`
		}
		return a.ExecuteChat(ctx, member, tools.NewChatCall("bob", "chat-1"), name, args(in))
	}

	DescribeTable("records how each call ended",
		func(name string, arrange func(), result, reason string, status int) {
			arrange()
			res, err := call(name)
			got, gotReason, gotStatus := tools.Classify(res, err)
			Expect([]any{got, gotReason, gotStatus}).To(Equal([]any{result, reason, status}), "classified from the answer")
			d := out.Data()
			Expect([]any{d.Result, d.Reason, d.Status}).To(Equal([]any{result, reason, status}), "recorded by the adapter")
			Expect(d.Binding).To(Equal("b-work"))
			Expect(d.Resource).To(Equal("acme/widgets"))
			// The record holds names and codes. No part of the arguments is in it.
			raw, mErr := json.Marshal(d)
			Expect(mErr).NotTo(HaveOccurred())
			Expect(string(raw)).NotTo(ContainSubstring(secretBody))
		},
		Entry("a read that works", read, func() {}, tools.OutcomeOK, "", 0),
		Entry("a write that is applied at once", write, func() {}, tools.OutcomeOK, "", 0),
		Entry("a write held because the connection only proposes", write, func() {
			f.proposal.AwaitsPerson, f.proposal.Hold = true, "write_mode_propose"
		}, tools.OutcomeHeld, "write_mode_propose", 0),
		Entry("a write held because the tool always asks", write, func() {
			f.proposal.AwaitsPerson, f.proposal.Hold = true, "approval_always"
		}, tools.OutcomeHeld, "approval_always", 0),
		Entry("a write the connection does not allow (write mode off)", write, func() {
			f.proposeErr = &gateway.CredentialRefusal{Code: gateway.CodeWriteNotAllowed}
		}, tools.OutcomeDenied, "write_mode_off", 0),
		Entry("a call with no connection chosen", read, func() {
			f.readErr = &gateway.CredentialRefusal{Code: gateway.CodeSelectConnection}
		}, tools.OutcomeDenied, "select_connection", 0),
		Entry("a resource that is not on the list of the connection", read, func() {
			f.readErr = &gateway.CredentialRefusal{Code: gateway.CodeResourceNotAllowed, Allowed: []string{"acme/other"}}
		}, tools.OutcomeDenied, "resource_not_allowed", 0),
		Entry("the person lacks a permission", read, func() { f.readErr = gateway.ErrNotPermitted }, tools.OutcomeDenied, "not_permitted", 0),
		Entry("the service refused the token (401)", write, func() {
			f.proposeErr = refused(401, "refused the token")
		}, tools.OutcomeError, "upstream_401", 401),
		Entry("the service refused the call for a missing permission (403)", write, func() {
			f.proposeErr = refused(403, "token cannot do this")
		}, tools.OutcomeError, "upstream_403", 403),
		Entry("the service could not find it (404)", read, func() { f.readErr = refused(404, "not found") }, tools.OutcomeError, "upstream_404", 404),
		Entry("the service did not accept the values (422)", write, func() {
			f.proposeErr = refused(422, "values not accepted")
		}, tools.OutcomeError, "upstream_422", 422),
		Entry("the add-on refused as invalid, with no status from the service", read, func() {
			f.readErr = gateway.Refused(&addonruntime.Refusal{Status: 400, Reason: "bad value"})
		}, tools.OutcomeError, "invalid_request", 0),
		Entry("the add-on is not running", read, func() { f.readErr = gateway.ErrNotSent }, tools.OutcomeError, "not_running", 0),
		Entry("an outage", read, func() { f.readErr = gateway.ErrUnavailable }, tools.OutcomeError, "unavailable", 0),
	)

	It("hands the model the status and the sentence of the add-on when the service refused", func() {
		f.proposeErr = refused(403, "GitHub refused: the token cannot create issues in this repository (403). Give the token Issues: write.")
		res, err := call(write)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultError))
		Expect(res.Code).To(Equal(tools.CodeUpstreamRefused))
		Expect(res.Upstream).To(Equal(403))
		Expect(res.Message).To(ContainSubstring("cannot create issues"))
		Expect(res.Note).To(ContainSubstring("Nothing was changed"))
		Expect(res.Note).To(ContainSubstring("do not report an outage"))
		Expect(res.JSON()).NotTo(ContainSubstring(secretBody))
	})
	It("falls back to the short reason when the add-on gave no sentence", func() {
		f.readErr = refused(404, "")
		res, err := call(read)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Code).To(Equal(tools.CodeUpstreamRefused))
		Expect(res.Message).To(Equal("the token has no permission for this"))
	})
	It("reports a refusal at the confirm of an immediate write the same way", func() {
		base.confirmErr = refused(422, "values not accepted")
		res, err := call(write)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Code).To(Equal(tools.CodeUpstreamRefused))
		Expect(res.Upstream).To(Equal(422))
		Expect(base.cancelled).To(HaveLen(1), "nothing waits for a browser after a certain failure")
	})
	It("marks a write that a rule of the host holds", func() {
		held := tools.HoldForPerson(ctx)
		res, err := a.ExecuteChat(held, member, tools.NewChatCall("bob", "chat-1"), write, args(`{"repo":"acme/widgets","body":"x"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultProposal))
		Expect(res.Proposal.Hold).To(Equal(tools.HoldByHost))
		Expect(out.Data().Reason).To(Equal(tools.HoldByHost))
	})
	It("keeps the kind of the tool", func() {
		_, err := call(write)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Data().Kind).To(Equal(contract.ToolKindWrite))
	})
	It("records nothing and fails nothing for a call with no outcome in the context", func() {
		_, err := a.ExecuteChat(context.Background(), member, tools.NewChatCall("bob", "chat-1"), read, args(`{"repo":"acme/widgets"}`))
		Expect(err).NotTo(HaveOccurred())
		var nilOutcome *tools.Outcome
		nilOutcome.Deny("x")
		Expect(nilOutcome.Data()).To(Equal(tools.OutcomeData{}))
	})
	It("classifies an error that is not a typed refusal as an error", func() {
		_, reason, _ := tools.Classify(tools.ToolResult{}, errors.New("boom"))
		Expect(reason).To(Equal("error"))
		Expect(strings.TrimSpace(reason)).NotTo(BeEmpty())
	})
})
