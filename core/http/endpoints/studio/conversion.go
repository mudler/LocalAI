package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/convert"
)

func totalsOf(r convert.Report) schema.ChatsConversionTotals {
	c := r.Counts()
	return schema.ChatsConversionTotals{Converted: c[convert.StatusConverted], Skipped: c[convert.StatusSkipped],
		Failed: c[convert.StatusFailed], Dropped: c[convert.StatusDropped]}
}

// Conversion returns what the conversion of the old data did for the caller.
// @Summary Report the conversion of your old agents, skills and chats.
// @Description At start the server converts the old chat page, agents, tasks, skills and Telegram connectors into chats, saved agents, triggers, skills and connector bindings. Triggers and connectors are made switched off. This lists the items that belong to you, with what was dropped and why; an administrator also gets the totals of all users. It never holds a token or the text of a prompt.
// @Tags chats
// @Success 200 {object} schema.ChatsConversion "The report"
// @Router /api/chats/conversion [get]
func (h *Handlers) Conversion(c echo.Context) error {
	rep, ok, err := h.svc.Conversion(c.Request().Context())
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsConversion{Done: ok, Items: []schema.ChatsConversionItem{}}
	if ok {
		own := rep.ForUser(userID(c))
		out.At = &rep.At
		out.Counts = totalsOf(own)
		for _, i := range own.Items {
			out.Items = append(out.Items, schema.ChatsConversionItem{Kind: i.Kind, Name: i.Name, Status: i.Status, Reason: i.Reason})
		}
		if isAdmin(c) {
			t := totalsOf(rep)
			out.Totals = &t
		}
	}
	return c.JSON(http.StatusOK, out)
}
