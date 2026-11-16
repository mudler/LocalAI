package localai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/nats-io/nkeys"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fixedNATS is the handover of a deployment with a NATS server.
type fixedNATS struct {
	url, ca   string
	clientTLS bool
	err       error
}

func (f fixedNATS) WorkerURL(context.Context) (string, error) { return f.url, f.err }
func (f fixedNATS) CAPEM() (string, error)                    { return f.ca, nil }
func (f fixedNATS) ClientTLS() bool                           { return f.clientTLS }

var _ = Describe("What a worker needs to follow a change of carrier", func() {
	var (
		ctx      context.Context
		registry *nodes.NodeRegistry
		natsCfg  natsauth.Config
		tunnels  *recordingDisconnector
	)

	stable := func(c cluster.Carrier) cluster.CarrierRow {
		return cluster.CarrierRow{Active: c, Epoch: 5, State: cluster.StateStable}
	}
	preparing := func(from, to cluster.Carrier) cluster.CarrierRow {
		return cluster.CarrierRow{Active: from, Epoch: 6, State: cluster.StatePrepare, Target: to}
	}
	handover := WithNATSHandover(fixedNATS{url: "nats://nats.example:4222", ca: "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----\n"})

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		registry, err = nodes.NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
		tunnels = &recordingDisconnector{}
		akp, err := nkeys.CreateAccount()
		Expect(err).ToNot(HaveOccurred())
		seed, err := akp.Seed()
		Expect(err).ToNot(HaveOccurred())
		natsCfg = natsauth.Config{AccountSeed: string(seed)}
	})

	register := func(body string, opts ...RegisterOption) map[string]any {
		GinkgoHelper()
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		opts = append(opts, WithTunnelDisconnector(tunnels))
		Expect(RegisterNodeEndpoint(registry, "", true, nil, "", natsCfg, opts...)(e.NewContext(req, rec))).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		return resp
	}
	storedHash := func(name string) string {
		GinkgoHelper()
		node, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		return node.TunnelTokenHash
	}

	Describe("the registration", func() {
		It("hands over the NATS address and the CA with the credential, when NATS is active", func() {
			resp := register(`{"name":"w","address":"10.0.0.1:50051"}`, WithCarrierReader(fixedCarrier{row: stable(cluster.CarrierNATS)}), handover)
			Expect(resp["nats_url"]).To(Equal("nats://nats.example:4222"))
			Expect(resp["nats_ca"]).To(ContainSubstring("BEGIN CERTIFICATE"))
			Expect(resp).To(HaveKey("nats_jwt"))
			Expect(resp["credential_for"]).To(Equal("nats"))
			Expect(resp).ToNot(HaveKey("nats_client_tls"))
		})

		It("says that the worker needs a certificate of its own when the server asks for one", func() {
			resp := register(`{"name":"w","address":"10.0.0.1:50051"}`, WithCarrierReader(fixedCarrier{row: stable(cluster.CarrierNATS)}),
				WithNATSHandover(fixedNATS{url: "tls://nats.example:4222", clientTLS: true}))
			Expect(resp["nats_client_tls"]).To(BeTrue())
		})

		It("hands over nothing of NATS when none is configured, and when the read fails", func() {
			reader := WithCarrierReader(fixedCarrier{row: stable(cluster.CarrierNATS)})
			Expect(register(`{"name":"a","address":"10.0.0.1:50051"}`, reader, WithNATSHandover(fixedNATS{}))).ToNot(HaveKey("nats_url"))
			Expect(register(`{"name":"b","address":"10.0.0.1:50051"}`, reader, WithNATSHandover(fixedNATS{err: errors.New("down")}))).ToNot(HaveKey("nats_url"))
		})

		It("does not hand over the address of NATS to a worker that asks for the tunnel", func() {
			resp := register(`{"name":"w","address":"10.0.0.1:50051","carrier":"tunnel"}`,
				WithCarrierReader(fixedCarrier{row: preparing(cluster.CarrierNATS, cluster.CarrierTunnel)}), handover)
			Expect(resp).To(HaveKey("tunnel_token"))
			Expect(resp).ToNot(HaveKey("nats_url"))
			Expect(resp["credential_for"]).To(Equal("tunnel"))
		})

		It("gives the credential of the target to a worker that asks for it while the change is prepared", func() {
			By("a tunnel worker that follows to NATS")
			first := register(`{"name":"w","address":"10.0.0.1:50051"}`, WithCarrierReader(fixedCarrier{row: stable(cluster.CarrierTunnel)}), handover)
			held := storedHash("w")
			Expect(first).To(HaveKey("tunnel_token"))
			before := len(tunnels.seen())

			resp := register(`{"name":"w","address":"10.0.0.1:50051","carrier":"nats"}`,
				WithCarrierReader(fixedCarrier{row: preparing(cluster.CarrierTunnel, cluster.CarrierNATS)}), handover)

			Expect(resp["carrier"]).To(Equal("tunnel"), "the active carrier is still the tunnel")
			Expect(resp["carrier_state"]).To(Equal("prepare"))
			Expect(resp["carrier_target"]).To(Equal("nats"))
			Expect(resp["credential_for"]).To(Equal("nats"))
			Expect(resp).To(HaveKey("nats_jwt"))
			Expect(resp).To(HaveKey("nats_url"))
			Expect(resp).ToNot(HaveKey("tunnel_token"))
			Expect(storedHash("w")).To(Equal(held), "the tunnel credential that the worker holds stays valid")
			Expect(tunnels.seen()).To(HaveLen(before), "and its session is not ended")
		})

		It("mints a tunnel credential for a worker on NATS that asks for the tunnel, and leaves its NATS credential alone", func() {
			resp := register(`{"name":"w","address":"10.0.0.1:50051","carrier":"tunnel"}`,
				WithCarrierReader(fixedCarrier{row: preparing(cluster.CarrierNATS, cluster.CarrierTunnel)}), handover)
			Expect(resp["carrier"]).To(Equal("nats"))
			Expect(resp["credential_for"]).To(Equal("tunnel"))
			Expect(resp["tunnel_token"]).ToNot(BeEmpty())
			Expect(resp).ToNot(HaveKey("nats_jwt"))
			Expect(storedHash("w")).ToNot(BeEmpty())
		})

		It("ignores a request for a carrier that is neither active, nor the target, nor draining", func() {
			resp := register(`{"name":"w","address":"10.0.0.1:50051","carrier":"tunnel"}`,
				WithCarrierReader(fixedCarrier{row: stable(cluster.CarrierNATS)}), handover)
			Expect(resp["credential_for"]).To(Equal("nats"))
			Expect(resp).ToNot(HaveKey("tunnel_token"), "a node cannot get a tunnel while the cluster has none")
			Expect(storedHash("w")).To(BeEmpty())

			resp = register(`{"name":"w","address":"10.0.0.1:50051","carrier":"nonsense"}`,
				WithCarrierReader(fixedCarrier{row: stable(cluster.CarrierNATS)}), handover)
			Expect(resp["credential_for"]).To(Equal("nats"))
		})

		It("gives the credential of the carrier that drains to a worker that asks for it", func() {
			until := time.Now().Add(time.Minute)
			row := cluster.CarrierRow{Active: cluster.CarrierTunnel, Epoch: 8, State: cluster.StateStable, Draining: cluster.CarrierNATS, DrainingUntil: &until}
			resp := register(`{"name":"w","address":"10.0.0.1:50051","carrier":"nats"}`, WithCarrierReader(fixedCarrier{row: row}), handover)
			Expect(resp["credential_for"]).To(Equal("nats"))
			Expect(resp["carrier_draining"]).To(Equal("nats"))
			Expect(resp).To(HaveKey("carrier_draining_until"))
			Expect(resp).To(HaveKey("nats_jwt"))
		})

		It("keeps the address of a worker that has none to offer the tunnel while it asks for NATS", func() {
			register(`{"name":"w","address":"10.0.0.1:50051","routable":true}`, WithCarrierReader(fixedCarrier{row: stable(cluster.CarrierTunnel)}), handover)
			register(`{"name":"w","address":"10.0.0.1:50051","routable":false,"carrier":"nats"}`,
				WithCarrierReader(fixedCarrier{row: preparing(cluster.CarrierTunnel, cluster.CarrierNATS)}), handover)
			node, err := registry.GetByName(ctx, "w")
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Address).To(Equal("10.0.0.1:50051"), "NATS dials the address, so it must not be dropped")
		})

		It("answers a deployment with no carrier reader as it always did, whatever the worker asks", func() {
			resp := register(`{"name":"w","address":"10.0.0.1:50051","carrier":"tunnel"}`)
			Expect(resp).ToNot(HaveKey("carrier"))
			Expect(resp).ToNot(HaveKey("credential_for"))
			Expect(resp).ToNot(HaveKey("tunnel_token"))
			Expect(resp).To(HaveKey("nats_jwt"))
		})
	})

	Describe("the heartbeat", func() {
		var id string

		BeforeEach(func() {
			id = register(`{"name":"w","address":"10.0.0.1:50051"}`)["id"].(string)
		})

		beat := func(body string, opts ...RegisterOption) (int, map[string]any) {
			GinkgoHelper()
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(id)
			Expect(HeartbeatEndpoint(registry, opts...)(c)).To(Succeed())
			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			return rec.Code, resp
		}

		It("tells the worker the carrier, the epoch, the state and the target of a change", func() {
			code, resp := beat(`{}`, WithCarrierReader(fixedCarrier{row: preparing(cluster.CarrierNATS, cluster.CarrierTunnel)}))
			Expect(code).To(Equal(http.StatusOK))
			Expect(resp["message"]).To(Equal("heartbeat received"))
			Expect(resp["carrier"]).To(Equal("nats"))
			Expect(resp["carrier_epoch"]).To(BeEquivalentTo(6))
			Expect(resp["carrier_state"]).To(Equal("prepare"))
			Expect(resp["carrier_target"]).To(Equal("tunnel"))
		})

		It("tells the worker that NATS asks for a client certificate", func() {
			_, resp := beat(`{}`, WithCarrierReader(fixedCarrier{row: stable(cluster.CarrierNATS)}), WithNATSHandover(fixedNATS{url: "tls://n:4222", clientTLS: true}))
			Expect(resp["nats_client_tls"]).To(BeTrue())
			_, resp = beat(`{}`, WithCarrierReader(fixedCarrier{row: stable(cluster.CarrierNATS)}), handover)
			Expect(resp).ToNot(HaveKey("nats_client_tls"))
		})

		It("answers as it always did when there is no carrier reader, or the row cannot be read", func() {
			_, resp := beat(`{}`)
			Expect(resp).To(Equal(map[string]any{"message": "heartbeat received"}))
			_, resp = beat(`{}`, WithCarrierReader(fixedCarrier{err: errors.New("down")}))
			Expect(resp).To(Equal(map[string]any{"message": "heartbeat received"}))
		})

		It("stores what the worker reports about its carriers", func() {
			code, _ := beat(`{"attached":["nats","tunnel"],"attached_epoch":6,"follow_capabilities":["nats","tunnel"],"follow_error":""}`)
			Expect(code).To(Equal(http.StatusOK))
			node, err := registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Attached).To(Equal("nats,tunnel"))
			Expect(node.AttachedEpoch).To(BeEquivalentTo(6))
			Expect(node.Follow).To(Equal("nats,tunnel"))

			beat(`{"attached":["tunnel"],"attached_epoch":7,"follow_capabilities":["tunnel"],"follow_error":"no address that a frontend can dial"}`)
			node, err = registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Attached).To(Equal("tunnel"))
			Expect(node.Follow).To(Equal("tunnel"))
			Expect(node.FollowError).To(Equal("no address that a frontend can dial"))
		})

		It("stores an empty attachment as a report, because the worker still reports what it can follow", func() {
			beat(`{"attached":["nats"],"follow_capabilities":["nats"]}`)
			beat(`{"attached":[],"follow_capabilities":["nats","tunnel"]}`)
			node, err := registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Attached).To(BeEmpty())
			Expect(node.Follow).To(Equal("nats,tunnel"))
		})

		It("stores nothing for a worker that predates the report, and leaves an earlier report alone", func() {
			beat(`{"available_ram":1024}`)
			node, err := registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Follow).To(BeEmpty())
			Expect(node.Attached).To(BeEmpty())
		})

		It("keeps only carriers that the cluster knows and cuts a reason that does not fit the column", func() {
			beat(`{"attached":["nats","gopher","nats"],"follow_capabilities":["tunnel","nope"],"follow_error":"` + strings.Repeat("é", 200) + `"}`)
			node, err := registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Attached).To(Equal("nats"))
			Expect(node.Follow).To(Equal("tunnel"))
			Expect(len(node.FollowError)).To(BeNumerically("<=", 255))
			Expect(node.FollowError).To(HavePrefix("é"))
		})

		It("answers 404 for a node that is not registered and stores nothing", func() {
			id = "no-such-node"
			code, _ := beat(`{"attached":["nats"],"follow_capabilities":["nats"]}`)
			Expect(code).To(Equal(http.StatusNotFound))
		})
	})
})
