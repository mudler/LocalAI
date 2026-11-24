package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type rolesTransport struct{ roles []contract.ConnectorRole }

func (t rolesTransport) Kinds(context.Context) ([]ConnectorKind, error) {
	return []ConnectorKind{{AddonID: "one", ConnectorKind: contract.ConnectorKind{ID: "chat", Title: "Chat", Roles: t.roles}}}, nil
}

func (rolesTransport) Call(context.Context, ConnectorCallScope, contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return contract.ConnectorResponse{}, nil
}

var _ = Describe("the roles of an inbound message", func() {
	roles := func(tr ConnectorTransport) *Connectors {
		svc, _ := newSkillService(&skillBroker{})
		st, err := connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		c, err := NewConnectors(svc, ConnectorOptions{Store: st, Transport: tr})
		Expect(err).NotTo(HaveOccurred())
		return c
	}
	b := connectors.Binding{ID: "b1", Kind: "chat", AddonID: "one", AgentID: "a1"}
	ctx := context.Background()

	It("keeps the roles that the kind declares and drops the others", func() {
		c := roles(rolesTransport{roles: []contract.ConnectorRole{{ID: "admin", Title: "Admin"}}})
		Expect(c.declaredRoles(ctx, b, []string{"admin", "root", "admin"})).To(Equal([]string{"admin"}))
	})
	It("drops all roles for a kind that declares none", func() {
		c := roles(rolesTransport{})
		Expect(c.declaredRoles(ctx, b, []string{"admin"})).To(BeEmpty())
	})
	It("drops all roles for a binding of another add-on's kind", func() {
		c := roles(rolesTransport{roles: []contract.ConnectorRole{{ID: "admin", Title: "Admin"}}})
		other := b
		other.AddonID = "two"
		Expect(c.declaredRoles(ctx, other, []string{"admin"})).To(BeEmpty())
	})
})
