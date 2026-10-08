package nodes

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The scheduler demotes a node on ErrNoRoute. Demoting on anything weaker
// condemns a node that is slow or busy, and a node that answers is present by
// demonstration, so a refusal must never look like a missing route.
var _ = Describe("Control request error classification", func() {
	const nodeID = "11111111-2222-3333-4444-555555555555"
	var (
		mc      *scriptedMessagingClient
		subject string
	)

	BeforeEach(func() {
		mc = newScriptedMessagingClient()
		subject = messaging.SubjectNodeBackendInstall(nodeID)
	})

	request := func() (*workerctl.BackendInstallReply, error) {
		return callVerb[workerctl.BackendInstallRequest, workerctl.BackendInstallReply](
			context.Background(), &natsLink{bus: mc}, nodeID, workerctl.VerbBackendInstall, workerctl.BackendInstallRequest{Backend: "b"}, time.Second)
	}

	It("reports a subject nobody answers as ErrNoRoute", func() {
		mc.scriptNoResponders(subject)
		_, err := request()
		Expect(errors.Is(err, ErrNoRoute)).To(BeTrue(), "got %v", err)
	})

	It("keeps the transport cause out of the unwrap chain", func() {
		mc.scriptNoResponders(subject)
		_, err := request()
		Expect(errors.Is(err, nats.ErrNoResponders)).To(BeFalse(),
			"consumers must match ErrNoRoute, never the carrier's own sentinel")
	})

	It("does not report a timeout as ErrNoRoute", func() {
		mc.scriptErr(subject, nats.ErrTimeout)
		_, err := request()
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
		Expect(isNATSTimeout(err)).To(BeTrue())
	})

	It("does not report a worker's own refusal as ErrNoRoute", func() {
		mc.scriptReply(subject, workerctl.BackendInstallReply{Success: false, Error: "disk full"})
		reply, err := request()
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeFalse())
		Expect(reply.Error).To(Equal("disk full"))
	})
})
