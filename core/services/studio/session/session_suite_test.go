package session_test

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSession(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio session suite")
}

// openAs opens a chat with the access of an actor that works on the whole
// home, as the product does for a chat without an agent folder.
func openAs(m *session.Manager, ctx context.Context, a workspace.Actor, chatID string) (*session.Chat, error) {
	x, err := m.UserAccess(ctx, a)
	if err != nil {
		return nil, err
	}
	return m.Open(ctx, x, chatID)
}
