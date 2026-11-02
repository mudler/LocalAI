package backend

import (
	"context"
	"github.com/mudler/LocalAI/core/services/music"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Music remote settlement gate", func() {
	It("denies unsupported unary settlement before inference", func() {
		lease, err := (RemoteMusicBackend{}).Admit(context.Background(), music.StageRequest{})
		Expect(lease).To(BeNil())
		Expect(err).To(MatchError(music.ErrSettlementUnsupported))
	})
})
