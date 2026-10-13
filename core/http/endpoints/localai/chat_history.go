package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/chathistory"
)

// ListConversationsEndpoint lists all stored conversations for the current user.
//
//	@Summary	List chat conversations
//	@Tags		chat-history
//	@Success	200	{object}	map[string]any
//	@Router		/api/conversations [get]
func ListConversationsEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		convs, err := store.List(getUserID(c))
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"conversations": convs})
	}
}

// GetConversationEndpoint returns a single conversation by ID.
//
//	@Summary	Get a chat conversation
//	@Tags		chat-history
//	@Param		id	path	string	true	"Conversation ID"
//	@Router		/api/conversations/{id} [get]
func GetConversationEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		conv, err := store.Get(getUserID(c), c.Param("id"))
		if err != nil {
			if errors.Is(err, chathistory.ErrNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "conversation not found"})
			}
			if errors.Is(err, chathistory.ErrInvalidID) {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid conversation id"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, conv)
	}
}

// SaveConversationEndpoint upserts a conversation. The body's id field is the
// canonical identifier; a path id is also accepted and overrides the body
// when both are present (so PUT /api/conversations/<id> works as expected).
//
//	@Summary	Save a chat conversation (upsert)
//	@Tags		chat-history
//	@Param		body	body	schema.Conversation	true	"Conversation payload"
//	@Router		/api/conversations [post]
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
			if errors.Is(err, chathistory.ErrInvalidID) {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid conversation id"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, saved)
	}
}

// BulkReplaceConversationsEndpoint replaces the entire conversation set for
// the current user — used by the React UI to migrate from localStorage on
// first connect.
//
//	@Summary	Replace all chat conversations
//	@Tags		chat-history
//	@Router		/api/conversations/bulk [put]
func BulkReplaceConversationsEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		var payload struct {
			Conversations []schema.Conversation `json:"conversations"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := store.ReplaceAll(getUserID(c), payload.Conversations); err != nil {
			if errors.Is(err, chathistory.ErrInvalidID) {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid conversation id"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"status": "ok", "count": len(payload.Conversations)})
	}
}

// DeleteConversationEndpoint removes a single conversation.
//
//	@Summary	Delete a chat conversation
//	@Tags		chat-history
//	@Param		id	path	string	true	"Conversation ID"
//	@Router		/api/conversations/{id} [delete]
func DeleteConversationEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := store.Delete(getUserID(c), c.Param("id")); err != nil {
			if errors.Is(err, chathistory.ErrNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "conversation not found"})
			}
			if errors.Is(err, chathistory.ErrInvalidID) {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid conversation id"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// DeleteAllConversationsEndpoint wipes the user's entire chat history.
//
//	@Summary	Delete all chat conversations for the current user
//	@Tags		chat-history
//	@Router		/api/conversations [delete]
func DeleteAllConversationsEndpoint(store *chathistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := store.DeleteAll(getUserID(c)); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}
