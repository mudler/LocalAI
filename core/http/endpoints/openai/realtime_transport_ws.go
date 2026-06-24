package openai

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/xlog"
)

// WebSocketTransport implements Transport over a gorilla/websocket connection.
type WebSocketTransport struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func NewWebSocketTransport(conn *websocket.Conn) *WebSocketTransport {
	return &WebSocketTransport{conn: conn}
}

func (t *WebSocketTransport) SendEvent(event types.ServerEvent) error {
	eventBytes, err := json.Marshal(event)
	if err != nil {
		xlog.Error("failed to marshal event", "error", err)
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn.WriteMessage(websocket.TextMessage, eventBytes)
}

func (t *WebSocketTransport) ReadEvent() ([]byte, error) {
	_, msg, err := t.conn.ReadMessage()
	return msg, err
}

// SendAudio is a no-op for WebSocket — audio is delivered via JSON events
// (base64-encoded in response.audio.delta).
func (t *WebSocketTransport) SendAudio(_ context.Context, _ []byte, _ int) error {
	return nil
}

func (t *WebSocketTransport) Close() error {
	return t.conn.Close()
}
