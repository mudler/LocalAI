package worker

import (
	"context"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
)

const serverPeer = "12D3KooWQYhTNQdmr3ArTeUHRYzFg94BKyTkoWBDWez9kSCVe2Xo"

// inviteLink is a well-formed invite link of kind k.
func inviteLink(k p2ppair.Kind) string {
	GinkgoHelper()
	p, err := p2pnet.DecodePeerID(serverPeer)
	Expect(err).ToNot(HaveOccurred())
	l := &p2ppair.Link{V: 1, Kind: k, Peer: p, Code: "ABCDE23456", Expires: time.Now().Add(time.Hour).Truncate(time.Second), Label: "worker"}
	return l.String()
}

var _ = Describe("joining a model-split worker over P2P", func() {
	It("refuses an invite of another kind before it creates anything", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "state")
		f := P2PJoinFlags{Join: inviteLink(p2ppair.KindDevice)}
		_, err := f.join(context.Background(), dir, "llama")
		Expect(err).To(MatchError(errNotSplitInvite))
		Expect(dir).ToNot(BeADirectory())
	})

	It("refuses a link that is not an invite", func() {
		f := P2PJoinFlags{Join: "https://example.org"}
		_, err := f.join(context.Background(), GinkgoT().TempDir(), "llama")
		Expect(err).To(MatchError(ContainSubstring("--join")))
	})

	It("keeps the pairing of one engine away from the other", func() {
		dir := GinkgoT().TempDir()
		Expect(writeJoinRecord(dir, &joinRecord{ServerPeer: serverPeer, DeviceID: "d1", Engine: "mlx"})).To(Succeed())
		f := P2PJoinFlags{Join: inviteLink(p2ppair.KindSplit)}
		_, err := f.join(context.Background(), dir, "llama")
		Expect(err).To(MatchError(ContainSubstring("--p2p-state-dir")))
	})

	It("needs a hostfile and a rank without --join", func() {
		r := &MLXDistributed{Rank: -1}
		if runtime.GOOS == "windows" {
			// The command refuses to start here, before it checks the options.
			Expect(r.Run(&cliContext.Context{})).To(MatchError(ContainSubstring("not available on Windows")))
			return
		}
		Expect(r.Run(&cliContext.Context{})).To(MatchError(ContainSubstring("--join")))
	})
})
