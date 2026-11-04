package studio

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var changeRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "change not found", proposals.ErrNotFound),
	verbatim(http.StatusBadRequest, typeInvalid, studiosvc.ErrBadChange.Error()+": ", studiosvc.ErrBadChange),
	verbatim(http.StatusConflict, typeConflict, "studio: ", studiosvc.ErrChangeExpired, studiosvc.ErrChangeAnswered,
		studiosvc.ErrTargetChanged, studiosvc.ErrNotApplied, studiosvc.ErrCannotRevert, studiosvc.ErrNothingToRevert),
	fixed(http.StatusConflict, typeFilesOff, "changes need files, which are off on this server", studiosvc.ErrChangesOff),
}

func failChange(c echo.Context, err error) error { return mapErr(c, err, changeRules, failFor) }

// ListChanges lists the change proposals.
// @Summary List the changes that a chat proposed.
// @Description A model proposes a change to your memory or an agent profile with the propose_change tool, from a Studio chat or a connector chat. Nothing is written until you confirm. A proposal expires after 24 hours. Newest first; status keeps only one status.
// @Tags chats
// @Param status query string false "pending, applied, dismissed, expired, reverted or stale"
// @Success 200 {array} contract.ChangeProposal "Your proposals"
// @Failure 400 {object} schema.ErrorResponse "The status is not known"
// @Router /api/chats/changes [get]
func (h *Handlers) ListChanges(c echo.Context) error {
	var statuses []contract.ProposalStatus
	if s := c.QueryParam("status"); s != "" {
		st := contract.ProposalStatus(s)
		switch st {
		case contract.ProposalPending, contract.ProposalApplied, contract.ProposalDismissed, contract.ProposalExpired, contract.ProposalReverted, contract.ProposalStale:
			statuses = append(statuses, st)
		default:
			return fail(c, http.StatusBadRequest, typeInvalid, "unknown status")
		}
	}
	list, err := h.svc.Changes().List(c.Request().Context(), userID(c), statuses...)
	if err != nil {
		return failChange(c, err)
	}
	if list == nil {
		list = []contract.ChangeProposal{}
	}
	return c.JSON(http.StatusOK, list)
}

// GetChange returns one change proposal.
// @Summary Get a proposed change.
// @Tags chats
// @Param id path string true "the proposal"
// @Success 200 {object} contract.ChangeProposal "The proposal, with the text it would write"
// @Failure 404 {object} schema.ErrorResponse "Not yours or unknown"
// @Router /api/chats/changes/{id} [get]
func (h *Handlers) GetChange(c echo.Context) error {
	p, err := h.svc.Changes().Get(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failChange(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// ConfirmChange writes a proposed change.
// @Summary Confirm a proposed change.
// @Description Writes the change to your files as one change set that Undo can take back. A JSON body {"content": "..."} writes your own text in place of the proposed one; it has the same size limit. It is refused with 409 when the file changed since the proposal, or when the proposal expired or was answered.
// @Tags chats
// @Param id path string true "the proposal"
// @Success 200 {object} contract.ChangeProposal "The applied proposal"
// @Failure 404 {object} schema.ErrorResponse "Not yours or unknown"
// @Failure 409 {object} schema.ErrorResponse "Stale, expired or answered"
// @Router /api/chats/changes/{id}/confirm [post]
func (h *Handlers) ConfirmChange(c echo.Context) error {
	var body struct {
		Content *string `json:"content"`
	}
	if c.Request().ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(c.Request().Body, 2*contract.MaxChangeBytes)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			return fail(c, http.StatusBadRequest, typeInvalid, "the body must be JSON")
		}
	}
	var p contract.ChangeProposal
	var err error
	if body.Content != nil {
		p, err = h.svc.Changes().ConfirmEdited(c.Request().Context(), userID(c), c.Param("id"), *body.Content)
	} else {
		p, err = h.svc.Changes().Confirm(c.Request().Context(), userID(c), c.Param("id"))
	}
	if err != nil {
		return failChange(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// DismissChange answers a proposed change with no.
// @Summary Dismiss a proposed change.
// @Tags chats
// @Param id path string true "the proposal"
// @Success 200 {object} contract.ChangeProposal "The dismissed proposal"
// @Failure 404 {object} schema.ErrorResponse "Not yours or unknown"
// @Failure 409 {object} schema.ErrorResponse "Not waiting"
// @Router /api/chats/changes/{id}/dismiss [post]
func (h *Handlers) DismissChange(c echo.Context) error {
	p, err := h.svc.Changes().Dismiss(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failChange(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// UndoChange reverts an applied change.
// @Summary Undo an applied change.
// @Description Restores the file with the change set the confirmation wrote. It is refused with 409 when the file was edited since.
// @Tags chats
// @Param id path string true "the proposal"
// @Success 200 {object} contract.ChangeProposal "The reverted proposal"
// @Failure 404 {object} schema.ErrorResponse "Not yours or unknown"
// @Failure 409 {object} schema.ErrorResponse "Not applied, or edited since"
// @Router /api/chats/changes/{id}/undo [post]
func (h *Handlers) UndoChange(c echo.Context) error {
	p, err := h.svc.Changes().Revert(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failChange(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// ChangeAudit lists the audit of the changes.
// @Summary List the audit of proposed changes.
// @Description Who proposed, confirmed, dismissed or reverted what, from which source, newest first. It holds a hash of the text, never the text.
// @Tags chats
// @Param limit query int false "most lines (default 50, at most 200)"
// @Success 200 {array} proposals.Event "Your audit lines"
// @Router /api/chats/changes/audit [get]
func (h *Handlers) ChangeAudit(c echo.Context) error {
	limit := 50
	if v, err := strconv.Atoi(c.QueryParam("limit")); err == nil && v > 0 {
		limit = min(v, 200)
	}
	evs, err := h.svc.Changes().Events(c.Request().Context(), userID(c), limit)
	if err != nil {
		return failChange(c, err)
	}
	return c.JSON(http.StatusOK, evs)
}

// ListContextFiles lists the notes the host puts in the prompt of a chat.
// @Summary List your context files.
// @Description The short index files that the host loads into every chat (your memory, an agent's notes, a conversation's notes), with the topic files next to them, the agent profiles and the caps that keep a turn small. It only reads; change a file on the Files page or confirm a proposal.
// @Tags chats
// @Success 200 {object} studiosvc.ContextFiles "Your context files"
// @Router /api/chats/context-files [get]
func (h *Handlers) ListContextFiles(c echo.Context) error {
	list, err := h.svc.ListContextFiles(c.Request().Context(), userID(c))
	if err != nil {
		return failChange(c, err)
	}
	return c.JSON(http.StatusOK, list)
}
