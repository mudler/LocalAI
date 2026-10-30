package sandbox_test

import (
	"context"
	"net"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type nopStore struct{ workspace.Store }

var _ = Describe("Spec", func() {
	It("applies the defaults and checks the fields", func() {
		h := func(context.Context, net.Conn) {}
		base := sandbox.Spec{Env: sandbox.Env{Store: nopStore{}}, View: workspace.View{Mounts: []workspace.Mount{{At: "w", Mode: workspace.ModeWrite}}}}
		s := base.WithDefaults()
		Expect(s.CPUs).To(Equal(2), "defaults = %+v", s)
		Expect(s.Memory).To(BeEquivalentTo(2<<30), "defaults = %+v", s)
		Expect(s.Disk).To(BeEquivalentTo(10<<30), "defaults = %+v", s)
		Expect(s.Check()).To(Succeed())
		bad := map[string]sandbox.Spec{}
		b := s
		b.Store = nil
		bad["no store"] = b
		b = s
		b.View = workspace.View{}
		bad["no mount"] = b
		b = s
		b.Memory = 1 << 20
		bad["small memory"] = b
		for _, ports := range []map[int]sandbox.PortHandler{{0: h}, {70000: h}, {sandbox.AgentPort: h}, {3128: nil}} {
			b = s
			b.Ports = ports
			bad["ports"] = b
			Expect(b.Check()).To(MatchError(workspace.ErrInvalid), "ports %v", ports)
		}
		for name, b := range bad {
			Expect(b.Check()).To(MatchError(workspace.ErrInvalid), name)
		}
	})
})
