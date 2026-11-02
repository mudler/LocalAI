// SPDX-License-Identifier: MIT
package schema

// ChatsMemoryCapture contains only safe, user-visible capture status.
type ChatsMemoryCapture struct {
	ID            string `json:"id"`
	Destination   string `json:"destination"`
	Status        string `json:"status"`
	EntryID       string `json:"entry_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
	NextRetry     int64  `json:"next_retry,omitempty"`
	RetryEligible bool   `json:"retry_eligible"`
}
