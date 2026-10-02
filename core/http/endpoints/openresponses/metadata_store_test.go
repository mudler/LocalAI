package openresponses

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("responseMetadataStoreAdapter", func() {
	var (
		db      *gorm.DB
		adapter *responseMetadataStoreAdapter
		ctx     context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		store, err := distributed.NewResponseMetadataStore(db)
		Expect(err).ToNot(HaveOccurred())
		adapter = &responseMetadataStoreAdapter{store: store}
	})

	It("round-trips every field of the replicated projection", func() {
		storedAt := time.Now().UTC().Truncate(time.Second)
		expiresAt := storedAt.Add(time.Hour)
		completedAt := storedAt.Unix()

		in := &syncedResponse{
			ID:           "resp_roundtrip",
			OwnerReplica: "replica-a",
			Owner:        "user-1",
			Request: &schema.OpenResponsesRequest{
				Model: "test-model",
				Input: "Hello",
			},
			Response: &schema.ORResponseResource{
				ID:          "resp_roundtrip",
				Object:      "response",
				CreatedAt:   storedAt.Unix(),
				CompletedAt: &completedAt,
				Status:      schema.ORStatusCompleted,
				Model:       "test-model",
				Output: []schema.ORItemField{
					{Type: "message", ID: "msg_roundtrip", Role: "assistant"},
				},
			},
			StoredAt:      storedAt,
			ExpiresAt:     &expiresAt,
			StreamEnabled: true,
			IsBackground:  true,
		}
		Expect(adapter.Upsert(ctx, in)).To(Succeed())

		out, err := adapter.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(1))
		got := out[0]

		// Asserted field by field rather than with one Equal on the struct: a
		// single Equal against a value whose Request is nil passes for an
		// adapter that dropped the field, and previous_response_id chaining is
		// exactly what that field feeds.
		Expect(got.ID).To(Equal(in.ID))
		Expect(got.OwnerReplica).To(Equal(in.OwnerReplica))
		Expect(got.Owner).To(Equal(in.Owner))
		Expect(got.Request).ToNot(BeNil())
		Expect(got.Request.Model).To(Equal("test-model"))
		Expect(got.Request.Input).To(Equal("Hello"))
		Expect(got.Response).ToNot(BeNil())
		Expect(got.Response.ID).To(Equal("resp_roundtrip"))
		Expect(got.Response.Status).To(Equal(schema.ORStatusCompleted))
		Expect(got.Response.Model).To(Equal("test-model"))
		Expect(got.Response.CompletedAt).ToNot(BeNil())
		Expect(*got.Response.CompletedAt).To(Equal(completedAt))
		Expect(got.Response.Output).To(HaveLen(1))
		Expect(got.Response.Output[0].ID).To(Equal("msg_roundtrip"))
		Expect(got.StoredAt.UTC()).To(BeTemporally("==", storedAt))
		Expect(got.ExpiresAt).ToNot(BeNil())
		Expect(got.ExpiresAt.UTC()).To(BeTemporally("==", expiresAt))
		Expect(got.StreamEnabled).To(BeTrue())
		Expect(got.IsBackground).To(BeTrue())
	})

	It("lifts ExpiresAt into the row's own column", func() {
		expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
		Expect(adapter.Upsert(ctx, &syncedResponse{ID: "resp_expiry", ExpiresAt: &expiresAt})).To(Succeed())

		// Read the column directly. The round-trip above is served entirely from
		// the JSON payload and stays green for an adapter that left this column
		// null, and a null here is what makes ListUnexpired and PurgeExpired
		// treat the response as immortal.
		var row distributed.ResponseMetadataRecord
		Expect(db.First(&row, "id = ?", "resp_expiry").Error).To(Succeed())
		Expect(row.ExpiresAt).ToNot(BeNil())
		Expect(row.ExpiresAt.UTC()).To(BeTemporally("==", expiresAt))
	})

	It("copies the owner columns out of the payload so an operator can filter on them", func() {
		Expect(adapter.Upsert(ctx, &syncedResponse{
			ID: "resp_owner", OwnerReplica: "replica-b", Owner: "user-9",
		})).To(Succeed())

		var row distributed.ResponseMetadataRecord
		Expect(db.First(&row, "id = ?", "resp_owner").Error).To(Succeed())
		Expect(row.OwnerReplica).To(Equal("replica-b"))
		Expect(row.Owner).To(Equal("user-9"))
	})

	It("does not return a row whose expiry has passed", func() {
		past := time.Now().Add(-time.Hour)
		Expect(adapter.Upsert(ctx, &syncedResponse{ID: "resp_dead", ExpiresAt: &past})).To(Succeed())
		Expect(adapter.Upsert(ctx, &syncedResponse{ID: "resp_live"})).To(Succeed())

		out, err := adapter.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(1))
		Expect(out[0].ID).To(Equal("resp_live"))
	})

	It("removes a deleted response from the durable rows", func() {
		Expect(adapter.Upsert(ctx, &syncedResponse{ID: "resp_gone"})).To(Succeed())
		Expect(adapter.Delete(ctx, "resp_gone")).To(Succeed())

		out, err := adapter.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(BeEmpty())
	})

	It("reports an unreachable database as an error and never as an empty list", func() {
		Expect(adapter.Upsert(ctx, &syncedResponse{ID: "resp_before_outage"})).To(Succeed())

		sqlDB, err := db.DB()
		Expect(err).ToNot(HaveOccurred())
		Expect(sqlDB.Close()).To(Succeed())

		// "No such response" and "the database could not be reached" are
		// different facts. An empty slice here would make a re-hydrate replace
		// the whole map with nothing, so a transient outage would 404 every
		// response on this replica.
		out, err := adapter.List(ctx)
		Expect(err).To(HaveOccurred())
		Expect(out).To(BeNil())
	})
})
