package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/chathistory"
)

// chatHistoryError maps store errors onto HTTP status codes. A deleted
// conversation is 410 Gone, so a client holding a stale copy can tell it
// apart from a transient failure and drop the copy instead of retrying.
func chatHistoryError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, chathistory.ErrInvalidID), errors.Is(err, chathistory.ErrInvalidUserID):
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, chathistory.ErrNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, chathistory.ErrDeleted):
		return c.JSON(http.StatusGone, map[string]string{"error": err.Error()})
	case errors.Is(err, chathistory.ErrConflict):
		return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// ListConversationsEndpoint lists the current user's conversations, plus the
// ids of conversations the user deleted (tombstones).
//
//	@Summary		List chat conversations
//	@Description	Returns the live conversations, newest first, and in `deleted` the conversations the user deleted. A client must drop any local copy whose id appears in `deleted` instead of uploading it again.
//	@Tags			chat-history
//	@Produce		json
//	@Success		200	{object}	schema.ConversationList
//	@Router			/api/conversations [get]
func ListConversationsEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		convs, err := store.List(userID)
		if err != nil {
			return chatHistoryError(c, err)
		}
		deleted, err := store.Tombstones(userID)
		if err != nil {
			return chatHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, schema.ConversationList{Conversations: convs, Deleted: deleted})
	}
}

// GetConversationEndpoint returns a single conversation by ID.
//
//	@Summary	Get a chat conversation
//	@Tags		chat-history
//	@Produce	json
//	@Param		id	path		string	true	"Conversation ID"
//	@Success	200	{object}	schema.Conversation
//	@Failure	404	{object}	map[string]string	"Never existed"
//	@Failure	410	{object}	map[string]string	"Deleted"
//	@Router		/api/conversations/{id} [get]
func GetConversationEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		conv, err := store.Get(getUserID(c), c.Param("id"))
		if err != nil {
			return chatHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, conv)
	}
}

// SaveConversationEndpoint upserts a conversation. The body's id field is the
// canonical identifier; a path id is also accepted and overrides the body
// when both are present (so PUT /api/conversations/<id> works as expected).
//
//	@Summary		Save a chat conversation (upsert)
//	@Description	Stores the conversation and returns it with its new server-assigned `version`. `createdAt`/`updatedAt` are kept as sent (Unix ms). Writing a conversation the user deleted returns 410 and does not bring it back.
//	@Tags			chat-history
//	@Accept			json
//	@Produce		json
//	@Param			body	body		schema.Conversation	true	"Conversation payload"
//	@Success		200		{object}	schema.Conversation
//	@Failure		400		{object}	map[string]string	"Invalid id or body"
//	@Failure		410		{object}	map[string]string	"Deleted"
//	@Router			/api/conversations [post]
func SaveConversationEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		var conv schema.Conversation
		if err := c.Bind(&conv); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if pathID := c.Param("id"); pathID != "" {
			conv.ID = pathID
		}
		saved, err := store.Save(getUserID(c), conv)
		if err != nil {
			return chatHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, saved)
	}
}

// SaveConversationByIDEndpoint is SaveConversationEndpoint under its own
// name so the PUT route gets its own swagger entry.
//
//	@Summary	Save a chat conversation by id (upsert)
//	@Tags		chat-history
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string				true	"Conversation ID (overrides the body id)"
//	@Param		body	body		schema.Conversation	true	"Conversation payload"
//	@Success	200		{object}	schema.Conversation
//	@Failure	400		{object}	map[string]string	"Invalid id or body"
//	@Failure	410		{object}	map[string]string	"Deleted"
//	@Router		/api/conversations/{id} [put]
func SaveConversationByIDEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return SaveConversationEndpoint(store)
}

// BulkReplaceConversationsEndpoint replaces the user's live conversation set
// in one transaction.
//
//	@Summary		Replace all chat conversations
//	@Description	Upserts every conversation in the payload and deletes the user's other live conversations. Ids the user deleted earlier are not written and are listed in `skipped`.
//	@Tags			chat-history
//	@Accept			json
//	@Produce		json
//	@Param			body	body		schema.ConversationBulkRequest	true	"Conversations"
//	@Success		200		{object}	schema.ConversationBulkResponse
//	@Failure		400		{object}	map[string]string	"Invalid id or body"
//	@Router			/api/conversations/bulk [put]
func BulkReplaceConversationsEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		var payload schema.ConversationBulkRequest
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		skipped, err := store.ReplaceAll(getUserID(c), payload.Conversations)
		if err != nil {
			return chatHistoryError(c, err)
		}
		if skipped == nil {
			skipped = []string{}
		}
		return c.JSON(http.StatusOK, schema.ConversationBulkResponse{
			Status:  "ok",
			Count:   len(payload.Conversations) - len(skipped),
			Skipped: skipped,
		})
	}
}

// DeleteConversationEndpoint deletes a single conversation, leaving a
// tombstone. Idempotent.
//
//	@Summary		Delete a chat conversation
//	@Description	Deletes the conversation and keeps a tombstone so no client can write it back. Deleting an unknown or already deleted id succeeds.
//	@Tags			chat-history
//	@Produce		json
//	@Param			id	path		string	true	"Conversation ID"
//	@Success		200	{object}	map[string]string
//	@Failure		400	{object}	map[string]string	"Invalid id"
//	@Router			/api/conversations/{id} [delete]
func DeleteConversationEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := store.Delete(getUserID(c), c.Param("id")); err != nil {
			return chatHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// DeleteAllConversationsEndpoint deletes the user's entire chat history.
//
//	@Summary	Delete all chat conversations for the current user
//	@Tags		chat-history
//	@Produce	json
//	@Success	200	{object}	map[string]string
//	@Router		/api/conversations [delete]
func DeleteAllConversationsEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := store.DeleteAll(getUserID(c)); err != nil {
			return chatHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}
