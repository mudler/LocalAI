package localai

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/modelupdates"
)

// ModelUpdatesProvider is the read model and the check of model updates
// (modelupdates.Service).
type ModelUpdatesProvider interface {
	Snapshot(ctx context.Context) modelupdates.Snapshot
	CheckNow(ctx context.Context) modelupdates.Snapshot
}

// ModelUpdatesEndpoint lists the installed gallery models and whether their
// gallery entry changed.
// @Summary List the model updates
// @Description Every model installed from a gallery, with its state: up-to-date, update-available, edited (the update would overwrite keys a person changed; editedKeys lists them), unknown (reason says why), gone (no gallery lists the entry), held (the owner holds this version) or failed (the last upgrade was rolled back; upgrade has the error). needsPerson marks what the Updates page counts: models never update automatically. What the galleries offer is as of the last check; the hold flag, an edit and an upgrade show at once. Every frontend of a cluster gives the same answer.
// @Tags models
// @Success 200 {object} modelupdates.Snapshot "Response"
// @Router /api/models/updates [get]
func ModelUpdatesEndpoint(p func() ModelUpdatesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		prov := p()
		if prov == nil {
			return c.JSON(http.StatusOK, disabledModelUpdates())
		}
		return c.JSON(http.StatusOK, prov.Snapshot(c.Request().Context()))
	}
}

// CheckModelUpdatesEndpoint checks the galleries now and answers when the
// check is done.
// @Summary Check for model updates now
// @Description Reads every gallery index again (not the copies of the last hour), compares each installed gallery model with its entry, waits for the check (at most 60 seconds) and returns the new data. One frontend of a cluster checks at a time; a request that arrives while another check runs waits for that check. A call within 15 seconds of the last check is answered from it with throttled set. A failed check shows as an error in the state of the gallery source and keeps the previous list. Installs nothing.
// @Tags models
// @Success 200 {object} modelupdates.Snapshot "Response"
// @Router /api/models/updates/check [post]
func CheckModelUpdatesEndpoint(p func() ModelUpdatesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		prov := p()
		if prov == nil {
			return c.JSON(http.StatusOK, disabledModelUpdates())
		}
		return c.JSON(http.StatusOK, prov.CheckNow(c.Request().Context()))
	}
}

func disabledModelUpdates() modelupdates.Snapshot {
	return modelupdates.Snapshot{
		Sources: map[string]backendupdates.Source{modelupdates.SourceGallery: {
			State: backendupdates.StateDisabled, Reason: "no model gallery is configured on this server",
		}},
		Items: []modelupdates.Item{},
	}
}
