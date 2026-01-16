package openresponses

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ResponseStore", func() {
	var store *ResponseStore

	BeforeEach(func() {
		store = NewResponseStore(0) // No TTL for most tests
	})

	AfterEach(func() {
		// Clean up
	})

	Describe("Store and Get", func() {
		It("should store and retrieve a response", func() {
			responseID := "resp_test123"
			request := &schema.OpenResponsesRequest{
				Model: "test-model",
				Input: "Hello",
			}
			response := &schema.ORResponseResource{
				ID:        responseID,
				Object:    "response",
				CreatedAt: time.Now().Unix(),
				Status:    "completed",
				Model:     "test-model",
				Output: []schema.ORItemField{
					{
						Type:   "message",
						ID:     "msg_123",
						Status: "completed",
						Role:   "assistant",
						Content: []schema.ORContentPart{{
							Type:        "output_text",
							Text:        "Hello, world!",
							Annotations: []schema.ORAnnotation{},
							Logprobs:    []schema.ORLogProb{},
						}},
					},
				},
			}

			store.Store(responseID, request, response)

			stored, err := store.Get(responseID)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored).ToNot(BeNil())
			Expect(stored.Response.ID).To(Equal(responseID))
			Expect(stored.Request.Model).To(Equal("test-model"))
			Expect(len(stored.Items)).To(Equal(1))
			Expect(stored.Items["msg_123"]).ToNot(BeNil())
			Expect(stored.Items["msg_123"].ID).To(Equal("msg_123"))
		})

		It("should return error for non-existent response", func() {
			_, err := store.Get("nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should index all items by ID", func() {
			responseID := "resp_test456"
			request := &schema.OpenResponsesRequest{
				Model: "test-model",
				Input: "Test",
			}
			response := &schema.ORResponseResource{
				ID:     responseID,
				Object: "response",
				Output: []schema.ORItemField{
					{
						Type:   "message",
						ID:     "msg_1",
						Status: "completed",
						Role:   "assistant",
					},
					{
						Type:      "function_call",
						ID:        "fc_1",
						Status:    "completed",
						CallID:    "fc_1",
						Name:      "test_function",
						Arguments: `{"arg": "value"}`,
					},
					{
						Type:   "message",
						ID:     "msg_2",
						Status: "completed",
						Role:   "assistant",
					},
				},
			}

			store.Store(responseID, request, response)

			stored, err := store.Get(responseID)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(stored.Items)).To(Equal(3))
			Expect(stored.Items["msg_1"]).ToNot(BeNil())
			Expect(stored.Items["fc_1"]).ToNot(BeNil())
			Expect(stored.Items["msg_2"]).ToNot(BeNil())
		})

		It("should handle items without IDs", func() {
			responseID := "resp_test789"
			request := &schema.OpenResponsesRequest{
				Model: "test-model",
				Input: "Test",
			}
			response := &schema.ORResponseResource{
				ID:     responseID,
				Object: "response",
				Output: []schema.ORItemField{
					{
						Type:   "message",
						ID:     "", // No ID
						Status: "completed",
						Role:   "assistant",
					},
					{
						Type:   "message",
						ID:     "msg_with_id",
						Status: "completed",
						Role:   "assistant",
					},
				},
			}

			store.Store(responseID, request, response)

			stored, err := store.Get(responseID)
			Expect(err).ToNot(HaveOccurred())
			// Only items with IDs are indexed
			Expect(len(stored.Items)).To(Equal(1))
			Expect(stored.Items["msg_with_id"]).ToNot(BeNil())
		})
	})

	Describe("GetItem", func() {
		It("should retrieve a specific item by ID", func() {
			responseID := "resp_item_test"
			itemID := "msg_specific"
			request := &schema.OpenResponsesRequest{
				Model: "test-model",
				Input: "Test",
			}
			response := &schema.ORResponseResource{
				ID:     responseID,
				Object: "response",
				Output: []schema.ORItemField{
					{
						Type:   "message",
						ID:     itemID,
						Status: "completed",
						Role:   "assistant",
						Content: []schema.ORContentPart{{
							Type:        "output_text",
							Text:        "Specific message",
							Annotations: []schema.ORAnnotation{},
							Logprobs:    []schema.ORLogProb{},
						}},
					},
				},
			}

			store.Store(responseID, request, response)

			item, err := store.GetItem(responseID, itemID)
			Expect(err).ToNot(HaveOccurred())
			Expect(item).ToNot(BeNil())
			Expect(item.ID).To(Equal(itemID))
			Expect(item.Type).To(Equal("message"))
		})

		It("should return error for non-existent item", func() {
			responseID := "resp_item_test2"
			request := &schema.OpenResponsesRequest{
				Model: "test-model",
				Input: "Test",
			}
			response := &schema.ORResponseResource{
				ID:     responseID,
				Object: "response",
				Output: []schema.ORItemField{
					{
						Type:   "message",
						ID:     "msg_existing",
						Status: "completed",
					},
				},
			}

			store.Store(responseID, request, response)

			_, err := store.GetItem(responseID, "nonexistent_item")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("item not found"))
		})

		It("should return error for non-existent response when getting item", func() {
			_, err := store.GetItem("nonexistent_response", "any_item")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("response not found"))
		})
	})

	Describe("FindItem", func() {
		It("should find an item across all stored responses", func() {
			// Store first response
			responseID1 := "resp_find_1"
			itemID1 := "msg_find_1"
			store.Store(responseID1, &schema.OpenResponsesRequest{Model: "test"}, &schema.ORResponseResource{
				ID:     responseID1,
				Object: "response",
				Output: []schema.ORItemField{
					{Type: "message", ID: itemID1, Status: "completed"},
				},
			})

			// Store second response
			responseID2 := "resp_find_2"
			itemID2 := "msg_find_2"
			store.Store(responseID2, &schema.OpenResponsesRequest{Model: "test"}, &schema.ORResponseResource{
				ID:     responseID2,
				Object: "response",
				Output: []schema.ORItemField{
					{Type: "message", ID: itemID2, Status: "completed"},
				},
			})

			// Find item from first response
			item, foundResponseID, err := store.FindItem(itemID1)
			Expect(err).ToNot(HaveOccurred())
			Expect(item).ToNot(BeNil())
			Expect(item.ID).To(Equal(itemID1))
			Expect(foundResponseID).To(Equal(responseID1))

			// Find item from second response
			item, foundResponseID, err = store.FindItem(itemID2)
			Expect(err).ToNot(HaveOccurred())
			Expect(item).ToNot(BeNil())
			Expect(item.ID).To(Equal(itemID2))
			Expect(foundResponseID).To(Equal(responseID2))
		})

		It("should return error when item not found in any response", func() {
			_, _, err := store.FindItem("nonexistent_item")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("item not found in any stored response"))
		})
	})

	Describe("Delete", func() {
		It("should delete a stored response", func() {
			responseID := "resp_delete_test"
			request := &schema.OpenResponsesRequest{Model: "test"}
			response := &schema.ORResponseResource{
				ID:     responseID,
				Object: "response",
			}

			store.Store(responseID, request, response)
			Expect(store.Count()).To(Equal(1))

			store.Delete(responseID)
			Expect(store.Count()).To(Equal(0))

			_, err := store.Get(responseID)
			Expect(err).To(HaveOccurred())
		})

		It("should handle deleting non-existent response gracefully", func() {
			// Should not panic
			store.Delete("nonexistent")
			Expect(store.Count()).To(Equal(0))
		})
	})

	Describe("Count", func() {
		It("should return correct count of stored responses", func() {
			Expect(store.Count()).To(Equal(0))

			store.Store("resp_1", &schema.OpenResponsesRequest{Model: "test"}, &schema.ORResponseResource{ID: "resp_1", Object: "response"})
			Expect(store.Count()).To(Equal(1))

			store.Store("resp_2", &schema.OpenResponsesRequest{Model: "test"}, &schema.ORResponseResource{ID: "resp_2", Object: "response"})
			Expect(store.Count()).To(Equal(2))

			store.Delete("resp_1")
			Expect(store.Count()).To(Equal(1))
		})
	})

	Describe("TTL and Expiration", func() {
		It("should set expiration when TTL is configured", func() {
			ttlStore := NewResponseStore(100 * time.Millisecond)
			responseID := "resp_ttl_test"
			request := &schema.OpenResponsesRequest{Model: "test"}
			response := &schema.ORResponseResource{ID: responseID, Object: "response"}

			ttlStore.Store(responseID, request, response)

			stored, err := ttlStore.Get(responseID)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.ExpiresAt).ToNot(BeNil())
			Expect(stored.ExpiresAt.After(time.Now())).To(BeTrue())
		})

		It("should not set expiration when TTL is 0", func() {
			responseID := "resp_no_ttl"
			request := &schema.OpenResponsesRequest{Model: "test"}
			response := &schema.ORResponseResource{ID: responseID, Object: "response"}

			store.Store(responseID, request, response)

			stored, err := store.Get(responseID)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.ExpiresAt).To(BeNil())
		})

		It("should clean up expired responses", func() {
			ttlStore := NewResponseStore(50 * time.Millisecond)
			responseID := "resp_expire_test"
			request := &schema.OpenResponsesRequest{Model: "test"}
			response := &schema.ORResponseResource{ID: responseID, Object: "response"}

			ttlStore.Store(responseID, request, response)
			Expect(ttlStore.Count()).To(Equal(1))

			// Wait for expiration (longer than TTL and cleanup interval)
			time.Sleep(150 * time.Millisecond)

			// Cleanup should remove expired response (may have already been cleaned by goroutine)
			count := ttlStore.Cleanup()
			// Count might be 0 if cleanup goroutine already ran, or 1 if we're first
			Expect(count).To(BeNumerically(">=", 0))
			Expect(ttlStore.Count()).To(Equal(0))

			_, err := ttlStore.Get(responseID)
			Expect(err).To(HaveOccurred())
		})

		It("should return error for expired response", func() {
			ttlStore := NewResponseStore(50 * time.Millisecond)
			responseID := "resp_expire_error"
			request := &schema.OpenResponsesRequest{Model: "test"}
			response := &schema.ORResponseResource{ID: responseID, Object: "response"}

			ttlStore.Store(responseID, request, response)

			// Wait for expiration (but not long enough for cleanup goroutine to remove it)
			time.Sleep(75 * time.Millisecond)

			// Try to get before cleanup goroutine removes it
			_, err := ttlStore.Get(responseID)
			// Error could be "expired" or "not found" (if cleanup already ran)
			Expect(err).To(HaveOccurred())
			// Either error message is acceptable
			errMsg := err.Error()
			Expect(errMsg).To(Or(ContainSubstring("expired"), ContainSubstring("not found")))
		})
	})

	Describe("Thread Safety", func() {
		It("should handle concurrent stores and gets", func() {
			// This is a basic concurrency test
			done := make(chan bool, 10)
			for i := 0; i < 10; i++ {
				go func(id int) {
					responseID := fmt.Sprintf("resp_concurrent_%d", id)
					request := &schema.OpenResponsesRequest{Model: "test"}
					response := &schema.ORResponseResource{
						ID:     responseID,
						Object: "response",
						Output: []schema.ORItemField{
							{Type: "message", ID: fmt.Sprintf("msg_%d", id), Status: "completed"},
						},
					}
					store.Store(responseID, request, response)

					// Retrieve immediately
					stored, err := store.Get(responseID)
					Expect(err).ToNot(HaveOccurred())
					Expect(stored).ToNot(BeNil())
					done <- true
				}(i)
			}

			// Wait for all goroutines
			for i := 0; i < 10; i++ {
				<-done
			}

			Expect(store.Count()).To(Equal(10))
		})
	})

	Describe("GetGlobalStore", func() {
		It("should return singleton instance", func() {
			store1 := GetGlobalStore()
			store2 := GetGlobalStore()
			Expect(store1).To(Equal(store2))
		})

		It("should persist data across GetGlobalStore calls", func() {
			globalStore := GetGlobalStore()
			responseID := "resp_global_test"
			request := &schema.OpenResponsesRequest{Model: "test"}
			response := &schema.ORResponseResource{ID: responseID, Object: "response"}

			globalStore.Store(responseID, request, response)

			// Get store again
			globalStore2 := GetGlobalStore()
			stored, err := globalStore2.Get(responseID)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored).ToNot(BeNil())
		})
	})
})
