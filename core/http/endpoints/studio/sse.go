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
	return streamSSEWithIDs(c, events, heartbeat, func(v T) (string, string, any) {
		name, data := frame(v)
		return "", name, data
	})
}

// streamSSEWithIDs is streamSSE with an event id on each event (none when id
// is empty), which a browser sends back as Last-Event-ID when it reconnects,
// and a heartbeat interval of the caller's choice.
func streamSSEWithIDs[T any](c echo.Context, events <-chan T, every time.Duration, frame func(T) (id, name string, data any)) error {
	w := c.Response()
	w.Header().Set(echo.HeaderContentType, "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.Flush()

	tick := time.NewTicker(every)
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
			id, name, data := frame(v)
			raw, err := json.Marshal(data)
			if err != nil {
				continue
			}
			head := ""
			if id != "" {
				head = "id: " + id + "\n"
			}
			if _, err := w.Write([]byte(head + "event: " + name + "\ndata: " + string(raw) + "\n\n")); err != nil {
				return nil
			}
			w.Flush()
		}
	}
}
