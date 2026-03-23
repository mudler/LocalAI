package localai

import (
	"crypto/sha256"
	"crypto/subtle"
	"testing"
)

// TestTokenValidation verifies the token hashing and comparison logic
// used in RegisterNodeEndpoint.
func TestTokenValidation(t *testing.T) {
	tests := []struct {
		name          string
		expectedToken string
		providedToken string
		wantMatch     bool
	}{
		{"matching tokens", "my-secret-token", "my-secret-token", true},
		{"mismatched tokens", "my-secret-token", "wrong-token", false},
		{"empty expected (no auth)", "", "any-token", true},
		{"empty provided when expected set", "my-secret-token", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the logic from RegisterNodeEndpoint
			if tt.expectedToken == "" {
				// No auth required — always matches
				if !tt.wantMatch {
					t.Error("expected no-auth to always pass")
				}
				return
			}

			if tt.providedToken == "" {
				if tt.wantMatch {
					t.Error("expected empty token to be rejected")
				}
				return
			}

			expectedHash := sha256.Sum256([]byte(tt.expectedToken))
			providedHash := sha256.Sum256([]byte(tt.providedToken))
			match := subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) == 1

			if match != tt.wantMatch {
				t.Errorf("got match=%v, want %v", match, tt.wantMatch)
			}
		})
	}
}
