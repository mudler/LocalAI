package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// wsEvent is a minimal representation of an ORStreamEvent for test assertions.
type wsEvent struct {
	Type           string          `json:"type"`
	SequenceNumber int             `json:"sequence_number"`
	Response       json.RawMessage `json:"response,omitempty"`
	Delta          *string         `json:"delta,omitempty"`
	ItemID         string          `json:"item_id,omitempty"`
	OutputIndex    *int            `json:"output_index,omitempty"`
	ContentIndex   *int            `json:"content_index,omitempty"`
	Item           json.RawMessage `json:"item,omitempty"`
	Error          *struct {
		Type    string `json:"type"`
		Code    string `json:"code,omitempty"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// wsResponseBody is a minimal representation of ORResponseResource for test assertions.
type wsResponseBody struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Model  string `json:"model"`
	Output []struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Role    string `json:"role,omitempty"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content,omitempty"`
	} `json:"output"`
}

func dialWS() (*websocket.Conn, error) {
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/v1/responses", apiPort)
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(wsURL, http.Header{})
	return conn, err
}

func readEvent(conn *websocket.Conn) (wsEvent, error) {
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var ev wsEvent
	err := conn.ReadJSON(&ev)
	return ev, err
}

func readAllEvents(conn *websocket.Conn) []wsEvent {
	var events []wsEvent
	for {
		ev, err := readEvent(conn)
		if err != nil {
			break
		}
		events = append(events, ev)
		if ev.Type == "response.completed" || ev.Type == "response.failed" {
			break
		}
	}
	return events
}

var _ = Describe("WebSocket Responses API E2E Tests", Label("WebSocket"), func() {
	Context("Basic response.create", func() {
		It("streams response events for a simple message", func() {
			conn, err := dialWS()
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			msg := map[string]interface{}{
				"type":  "response.create",
				"model": "mock-model",
				"input": []map[string]interface{}{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]interface{}{
							{"type": "input_text", "text": "Hello"},
						},
					},
				},
			}
			Expect(conn.WriteJSON(msg)).To(Succeed())

			events := readAllEvents(conn)
			Expect(events).ToNot(BeEmpty())

			// Verify event sequence
			typesSeen := make([]string, 0, len(events))
			for _, ev := range events {
				typesSeen = append(typesSeen, ev.Type)
			}

			Expect(typesSeen).To(ContainElement("response.created"))
			Expect(typesSeen).To(ContainElement("response.in_progress"))
			Expect(typesSeen).To(ContainElement("response.output_item.added"))
			Expect(typesSeen).To(ContainElement("response.output_text.delta"))
			Expect(typesSeen).To(ContainElement("response.completed"))

			// Verify sequence numbers are monotonically increasing
			for i := 1; i < len(events); i++ {
				Expect(events[i].SequenceNumber).To(BeNumerically(">", events[i-1].SequenceNumber))
			}

			// Verify the completed response has content
			last := events[len(events)-1]
			Expect(last.Type).To(Equal("response.completed"))

			var resp wsResponseBody
			Expect(json.Unmarshal(last.Response, &resp)).To(Succeed())
			Expect(resp.Status).To(Equal("completed"))
			Expect(resp.Model).To(Equal("mock-model"))
			Expect(resp.Output).ToNot(BeEmpty())
		})
	})

	Context("Continuation with previous_response_id", func() {
		It("chains responses using previous_response_id", func() {
			conn, err := dialWS()
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			// First turn
			msg1 := map[string]interface{}{
				"type":  "response.create",
				"model": "mock-model",
				"store": true,
				"input": []map[string]interface{}{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]interface{}{
							{"type": "input_text", "text": "Hello"},
						},
					},
				},
			}
			Expect(conn.WriteJSON(msg1)).To(Succeed())

			events1 := readAllEvents(conn)
			Expect(events1).ToNot(BeEmpty())

			// Extract response ID from response.completed
			var firstResp wsResponseBody
			for _, ev := range events1 {
				if ev.Type == "response.completed" {
					Expect(json.Unmarshal(ev.Response, &firstResp)).To(Succeed())
				}
			}
			Expect(firstResp.ID).ToNot(BeEmpty())

			// Second turn with previous_response_id
			msg2 := map[string]interface{}{
				"type":                 "response.create",
				"model":                "mock-model",
				"previous_response_id": firstResp.ID,
				"input": []map[string]interface{}{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]interface{}{
							{"type": "input_text", "text": "Follow up question"},
						},
					},
				},
			}
			Expect(conn.WriteJSON(msg2)).To(Succeed())

			events2 := readAllEvents(conn)
			Expect(events2).ToNot(BeEmpty())

			// Verify second response completed
			hasCompleted := false
			for _, ev := range events2 {
				if ev.Type == "response.completed" {
					hasCompleted = true
					var secondResp wsResponseBody
					Expect(json.Unmarshal(ev.Response, &secondResp)).To(Succeed())
					Expect(secondResp.Status).To(Equal("completed"))
					// Should be a different response ID
					Expect(secondResp.ID).ToNot(Equal(firstResp.ID))
				}
			}
			Expect(hasCompleted).To(BeTrue())
		})
	})

	Context("Error handling", func() {
		It("returns error for previous_response_not_found", func() {
			conn, err := dialWS()
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			msg := map[string]interface{}{
				"type":                 "response.create",
				"model":                "mock-model",
				"previous_response_id": "resp_nonexistent",
				"input":                "Hello",
			}
			Expect(conn.WriteJSON(msg)).To(Succeed())

			ev, err := readEvent(conn)
			Expect(err).ToNot(HaveOccurred())
			Expect(ev.Type).To(Equal("error"))
			Expect(ev.Error).ToNot(BeNil())
			Expect(ev.Error.Code).To(Equal("previous_response_not_found"))
		})

		It("returns error for unsupported message type", func() {
			conn, err := dialWS()
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			msg := map[string]interface{}{
				"type": "unknown.type",
			}
			Expect(conn.WriteJSON(msg)).To(Succeed())

			ev, err := readEvent(conn)
			Expect(err).ToNot(HaveOccurred())
			Expect(ev.Type).To(Equal("error"))
			Expect(ev.Error).ToNot(BeNil())
			Expect(ev.Error.Message).To(ContainSubstring("unsupported message type"))
		})

		It("returns error for missing model", func() {
			conn, err := dialWS()
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			msg := map[string]interface{}{
				"type":  "response.create",
				"input": "Hello",
			}
			Expect(conn.WriteJSON(msg)).To(Succeed())

			ev, err := readEvent(conn)
			Expect(err).ToNot(HaveOccurred())
			Expect(ev.Type).To(Equal("error"))
			Expect(ev.Error).ToNot(BeNil())
			Expect(ev.Error.Message).To(ContainSubstring("model is required"))
		})
	})

	Context("Multiple turns on same connection", func() {
		It("handles sequential requests on a single connection", func() {
			conn, err := dialWS()
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			for i := 0; i < 3; i++ {
				msg := map[string]interface{}{
					"type":  "response.create",
					"model": "mock-model",
					"input": []map[string]interface{}{
						{
							"type": "message",
							"role": "user",
							"content": []map[string]interface{}{
								{"type": "input_text", "text": fmt.Sprintf("Message %d", i)},
							},
						},
					},
				}
				Expect(conn.WriteJSON(msg)).To(Succeed())

				events := readAllEvents(conn)
				Expect(events).ToNot(BeEmpty())

				hasCompleted := false
				for _, ev := range events {
					if ev.Type == "response.completed" {
						hasCompleted = true
					}
				}
				Expect(hasCompleted).To(BeTrue(), "turn %d should complete", i)
			}
		})
	})

	Context("Text deltas", func() {
		It("accumulates deltas into the full response text", func() {
			conn, err := dialWS()
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			msg := map[string]interface{}{
				"type":  "response.create",
				"model": "mock-model",
				"input": []map[string]interface{}{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]interface{}{
							{"type": "input_text", "text": "Hello"},
						},
					},
				},
			}
			Expect(conn.WriteJSON(msg)).To(Succeed())

			events := readAllEvents(conn)

			// Collect all text deltas
			accumulated := ""
			for _, ev := range events {
				if ev.Type == "response.output_text.delta" && ev.Delta != nil {
					accumulated += *ev.Delta
				}
			}

			// The mock backend streams "This is a mocked streaming response." char by char
			Expect(accumulated).To(ContainSubstring("mocked"))
		})
	})
})
