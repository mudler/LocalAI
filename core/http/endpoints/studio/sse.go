package studio

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

// streamSSE writes the values of events as server-sent events until the client
// goes away or the channel closes. A closed channel means the subscriber fell
// behind; the client reconnects and reads what it missed. frame gives the event
// name and the value to encode. A value that cannot be encoded is skipped. A
// heartbeat comment keeps idle proxies from closing the stream.
func streamSSE[T any](c echo.Context, events <-chan T, frame func(T) (name string, data any)) error {
	w := c.Response()
	w.Header().Set(echo.HeaderContentType, "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.Flush()

	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-c.Request().Context().Done():
			return nil
		case <-tick.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return nil
			}
			w.Flush()
		case v, ok := <-events:
			if !ok {
				return nil
			}
			name, data := frame(v)
			raw, err := json.Marshal(data)
			if err != nil {
				continue
			}
			if _, err := w.Write([]byte("event: " + name + "\ndata: " + string(raw) + "\n\n")); err != nil {
				return nil
			}
			w.Flush()
		}
	}
}
