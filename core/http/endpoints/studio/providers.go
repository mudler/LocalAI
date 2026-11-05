package studio

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
)

var providerRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "unknown provider slot", studiosvc.ErrUnknownSlot),
	sentinelText(http.StatusBadRequest, typeInvalid, studiosvc.ErrUnknownProvider),
	sentinelText(http.StatusConflict, typeConflict, studiosvc.ErrProviderUnavailable),
	fixed(http.StatusServiceUnavailable, "unavailable", "the choice of provider cannot be stored on this server", studiosvc.ErrProviderSettings),
}

func providerSlotOut(v studiosvc.SlotView) schema.ChatsProviderSlot {
	r := v.Resolution
	out := schema.ChatsProviderSlot{ID: v.ID, Version: v.Version, Title: v.Title, Description: v.Description, OffMeans: v.OffMeans,
		Providers: make([]schema.ChatsProvider, 0, len(v.Providers)),
		Resolution: schema.ChatsProviderResolution{Slot: r.Slot, Selected: r.Selected, Stored: r.Stored, Active: r.Active, Off: r.Off, Code: r.Code,
			Reason: r.Reason, AddonID: r.AddonID, AddonTitle: r.AddonTitle}}
	if h := v.Health; h.CoolingDown {
		out.Health = &schema.ChatsProviderHealth{CoolingDown: true, RetryAt: h.RetryAt.UTC().Format(time.RFC3339), Last: h.Last, Notice: h.Notice}
	}
	for _, p := range v.Providers {
		out.Providers = append(out.Providers, schema.ChatsProvider{ID: p.ID, Title: p.Title, Description: p.Description, Kind: p.Kind,
			Available: p.Available, Properties: p.Properties, Roles: p.Roles, Selected: p.Selected})
	}
	return out
}

func failProvider(c echo.Context, err error) error { return mapErr(c, err, providerRules, failFor) }

// ListProviders lists the provider slots and who can serve each.
// @Summary List the provider slots and their providers.
// @Description A provider slot is a job the host does itself and lets an add-on do in its place (memory is the first). Each slot lists the built-in provider, every installed add-on that declares the slot in capabilities.provides, and Off when the slot allows it, with the choice in force. resolution.active is what serves now: builtin, an add-on ID or off. A picked add-on that is stopped, disabled or removed does not fall back to the built-in provider: the slot is off, with resolution.code provider_unavailable and the add-on named, until you pick again. Only the add-on you picked has its context files, skills and role tools in your chats.
// @Tags chats
// @Success 200 {object} schema.ChatsProviderSlots "The slots and their providers"
// @Router /api/chats/providers [get]
func (h *Handlers) ListProviders(c echo.Context) error {
	slots, err := h.svc.ProviderSlots(c.Request().Context(), userID(c))
	if err != nil {
		return failProvider(c, err)
	}
	out := schema.ChatsProviderSlots{Slots: make([]schema.ChatsProviderSlot, 0, len(slots))}
	for _, v := range slots {
		out.Slots = append(out.Slots, providerSlotOut(v))
	}
	return c.JSON(http.StatusOK, out)
}

// GetProvider reads one provider slot.
// @Summary Read one provider slot.
// @Tags chats
// @Param slot path string true "the slot, such as memory"
// @Success 200 {object} schema.ChatsProviderSlot "The slot with its providers and your choice"
// @Failure 404 {object} schema.ErrorResponse "Unknown slot"
// @Router /api/chats/providers/{slot} [get]
func (h *Handlers) GetProvider(c echo.Context) error {
	v, err := h.svc.ProviderSlot(c.Request().Context(), userID(c), c.Param("slot"))
	if err != nil {
		return failProvider(c, err)
	}
	return c.JSON(http.StatusOK, providerSlotOut(v))
}

// PutProvider picks the provider of a slot.
// @Summary Pick the provider of a slot.
// @Description Sets your provider for the slot: builtin, off (when the slot allows it) or the ID of an add-on that declares the slot and runs now. The choice is kept with your account, so every frontend of a cluster reads the same answer. 400 for a provider the slot does not list, 409 for an add-on that cannot serve now.
// @Tags chats
// @Accept json
// @Param slot path string true "the slot, such as memory"
// @Param request body schema.ChatsProviderRequest true "the provider to pick"
// @Success 200 {object} schema.ChatsProviderSlot "The slot as it stands now"
// @Failure 400 {object} schema.ErrorResponse "The slot does not list this provider"
// @Failure 404 {object} schema.ErrorResponse "Unknown slot"
// @Failure 409 {object} schema.ErrorResponse "The add-on cannot serve now"
// @Failure 503 {object} schema.ErrorResponse "The choice cannot be stored on this server"
// @Router /api/chats/providers/{slot} [put]
func (h *Handlers) PutProvider(c echo.Context) error {
	req, ok := bind[schema.ChatsProviderRequest](c, "the body must be {provider}")
	if !ok {
		return nil
	}
	v, err := h.svc.SelectProvider(c.Request().Context(), userID(c), c.Param("slot"), req.Provider)
	if err != nil {
		return failProvider(c, err)
	}
	return c.JSON(http.StatusOK, providerSlotOut(v))
}
