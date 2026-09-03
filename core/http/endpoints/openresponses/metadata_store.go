package openresponses

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/syncstate"
)

// responseMetadataStoreAdapter bridges the durable store onto the generic
// syncstate.Store the responses.metadata map consumes. It mirrors
// finetune.fineTuneStoreAdapter and agentpool.taskStoreAdapter, which is the
// shape every other adopter already uses.
//
// It lives in this package rather than beside the store because syncedResponse
// is unexported: the projection of a response that a peer may act on is defined
// here and nowhere else, and the durable row carries it as opaque JSON so the
// two cannot drift.
type responseMetadataStoreAdapter struct {
	store *distributed.ResponseMetadataStore
}

// compile-time assertion that the adapter satisfies the component's Store.
var _ syncstate.Store[string, *syncedResponse] = (*responseMetadataStoreAdapter)(nil)

// List re-hydrates the map from the durable rows that are still live.
//
// A decode failure is returned rather than skipped. Skipping would drop exactly
// one response with nothing failing anywhere, which is the invisible-404 this
// whole store exists to prevent; returning the error leaves the map holding
// whatever it already had, because syncstate.hydrate replaces nothing when the
// source errors.
func (a *responseMetadataStoreAdapter) List(ctx context.Context) ([]*syncedResponse, error) {
	records, err := a.store.ListUnexpired(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*syncedResponse, 0, len(records))
	for i := range records {
		v := &syncedResponse{}
		if err := json.Unmarshal(records[i].PayloadJSON, v); err != nil {
			return nil, fmt.Errorf("decoding replicated response metadata %q: %w", records[i].ID, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// Upsert writes the whole projection as JSON, plus the columns a hydrate and a
// purge filter on.
//
// ExpiresAt is lifted out of the payload into its own column because
// ListUnexpired and PurgeExpired compare against it on the database clock.
//
// It is null whenever the deployment runs the default Open Responses TTL of 0,
// which is the ordinary case and not an error: the store then falls back to
// distributed.DefaultResponseMetadataRetention, so the row is still swept and
// still drops out of a hydrate. What the column buys is the other direction. A
// deployment that DOES configure a TTL gets that TTL honoured here, rather than
// having its responses outlive the map they mirror or die before it.
func (a *responseMetadataStoreAdapter) Upsert(ctx context.Context, v *syncedResponse) error {
	if v == nil {
		return fmt.Errorf("replicating response metadata: nil value")
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding replicated response metadata %q: %w", v.ID, err)
	}
	return a.store.Upsert(ctx, &distributed.ResponseMetadataRecord{
		ID:           v.ID,
		OwnerReplica: v.OwnerReplica,
		Owner:        v.Owner,
		PayloadJSON:  payload,
		ExpiresAt:    v.ExpiresAt,
	})
}

func (a *responseMetadataStoreAdapter) Delete(ctx context.Context, k string) error {
	return a.store.Delete(ctx, k)
}
