package agentpool

import (
	"fmt"
	"math/rand"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAGI/core/sse"
)

// HandleSSE bridges a LocalAGI SSE Manager to an Echo HTTP response.
// It registers a client with the manager, streams events, and cleans up on disconnect.
func HandleSSE(c echo.Context, manager sse.Manager) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(200)
	c.Response().Flush()

	client := sse.NewClient(randString(10))
	manager.Register(client)
	defer func() {
		manager.Unregister(client.ID())
	}()

	ch := client.Chan()
	done := c.Request().Context().Done()

	for {
		select {
		case <-done:
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			if _, err := fmt.Fprint(c.Response(), msg.String()); err != nil {
				return nil
			}
			c.Response().Flush()
		}
	}
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}
