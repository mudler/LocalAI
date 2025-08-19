package p2p

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services"

	"github.com/mudler/edgevpn/pkg/node"
	zlog "github.com/rs/zerolog/log"
)

func syncState(ctx context.Context, n *node.Node, app *application.Application) error {
	zlog.Info().Msg("Syncing state")

	whatWeHave := []string{}
	for _, model := range app.ModelConfigLoader().GetAllModelsConfigs() {
		whatWeHave = append(whatWeHave, model.Name)
	}

	ledger, _ := n.Ledger()
	data, exists := ledger.GetKey("shared_state", "models")
	if !exists {
		zlog.Info().Msg("No models found")
		// we announce ours
		ledger.AnnounceUpdate(ctx, time.Minute, "shared_state", "models", whatWeHave)
		zlog.Info().Msgf("Announced our models: %v", whatWeHave)
	}

	models := []string{}
	data.Unmarshal(models)

	// Sync with our state
	whatIsNotThere := []string{}
	for _, model := range whatWeHave {
		if !slices.Contains(models, model) {
			whatIsNotThere = append(whatIsNotThere, model)
		}
	}
	if len(whatIsNotThere) > 0 {
		zlog.Info().Msgf("Announcing our models: %v", append(models, whatIsNotThere...))
		ledger.AnnounceUpdate(ctx, time.Minute, "shared_state", "models", append(models, whatIsNotThere...))
	}

	// Check if we have a model that is not in our state, otherwise install it
	for _, model := range models {
		if slices.Contains(whatWeHave, model) {
			continue
		}

		// we install model
		zlog.Info().Msgf("Installing model: %s", model)

		uuid, err := uuid.NewUUID()
		if err != nil {
			zlog.Error().Err(err).Msg("error generating UUID")
			continue
		}

		app.GalleryService().ModelGalleryChannel <- services.GalleryOp[gallery.GalleryModel]{
			ID:                 uuid.String(),
			GalleryElementName: model,
			Galleries:          app.ApplicationConfig().Galleries,
			BackendGalleries:   app.ApplicationConfig().BackendGalleries,
		}
	}

	return nil
}

func Sync(ctx context.Context, n *node.Node, app *application.Application) error {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Minute):
				if err := syncState(ctx, n, app); err != nil {
					zlog.Error().Err(err).Msg("error syncing state")
				}
			}

		}
	}()
	return nil
}
