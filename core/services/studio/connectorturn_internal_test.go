package studio

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// titleTransport serves one kind and answers no call.
type titleTransport struct{}

func (titleTransport) Kinds(context.Context) ([]ConnectorKind, error) {
	return []ConnectorKind{{AddonID: "slack-addon", ConnectorKind: contract.ConnectorKind{ID: "slack", Title: "Slack",
		Files: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 2 << 20}}}}}, nil
}

func (titleTransport) Call(context.Context, ConnectorCallScope, contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return contract.ConnectorResponse{}, nil
}

var _ = Describe("the turn block of a connector chat", func() {
	var (
		ctx context.Context
		svc *Service
		st  connectors.Store
	)
	BeforeEach(func() {
		ctx = context.Background()
		svc, _ = newSkillService(&skillBroker{})
		var err error
		st, err = connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		c, err := NewConnectors(svc, ConnectorOptions{Store: st, Transport: titleTransport{}})
		Expect(err).NotTo(HaveOccurred())
		svc.SetConnectors(c)
	})

	// prompt builds the host prompt of two turns of a chat and returns both.
	prompt := func(origin Origin) (string, string) {
		rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m"}, origin)
		Expect(err).NotTo(HaveOccurred())
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		first, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		second, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		return first.Prompt, second.Prompt
	}
	bind := func(id string, filesOff bool) {
		Expect(st.PutBinding(ctx, connectors.Binding{ID: id, Owner: "alice", Kind: "slack", AddonID: "slack-addon", FilesOff: filesOff})).To(Succeed())
	}

	It("tells the model that files are attached and what the limits are", func() {
		bind("b1", false)
		p1, p2 := prompt(Origin{Kind: session.OriginConnector, ID: "b1"})
		Expect(p1).To(ContainSubstring("This chat is on Slack."))
		Expect(p1).To(ContainSubstring("attached to your answer automatically"))
		Expect(p1).To(ContainSubstring("call attach_file with its path"), "the way to give a file that exists")
		Expect(p1).To(ContainSubstring("never tell them a path"))
		Expect(p1).To(ContainSubstring("Up to 5 files per turn, 2 MiB (limit of Slack) each"))
		Expect(p1).NotTo(ContainSubstring("NOT sent"))
		Expect(strings.Count(p2, "This chat is on")).To(Equal(1), "a second turn does not repeat the block")
		Expect(p2).To(Equal(p1))
		Expect(len(svc.connectorBlock(ctx, session.ChatRecord{Origin: session.OriginConnector, OriginID: "b1"}, true))).To(BeNumerically("<", 700), "the block is read at each turn")
	})

	It("says that files are not sent when the binding turns them off", func() {
		bind("b2", true)
		p, _ := prompt(Origin{Kind: session.OriginConnector, ID: "b2"})
		Expect(p).To(ContainSubstring("This chat is on Slack."))
		Expect(p).To(ContainSubstring("Files are NOT sent"))
		Expect(p).NotTo(ContainSubstring("attached to your answer automatically"))
		Expect(p).NotTo(ContainSubstring("attach_file"), "a binding that sends no file has no such tool")
	})

	It("leaves out the file part when the binding cannot be read", func() {
		p, _ := prompt(Origin{Kind: session.OriginConnector, ID: "gone"})
		Expect(p).To(ContainSubstring("This chat is on a messaging app."))
		Expect(p).NotTo(ContainSubstring("attached to your answer"))
		Expect(p).NotTo(ContainSubstring("NOT sent"))
	})

	It("adds nothing to a chat a person opens in Studio", func() {
		bind("b3", false)
		p, _ := prompt(Origin{})
		Expect(p).NotTo(ContainSubstring("This chat is on"))
	})
})
