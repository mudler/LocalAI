package sandbox_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

type nopStore struct{ workspace.Store }

func TestSpecDefaultsAndCheck(t *testing.T) {
	h := func(context.Context, net.Conn) {}
	base := sandbox.Spec{Env: sandbox.Env{Store: nopStore{}}, View: workspace.View{Mounts: []workspace.Mount{{At: "w", Mode: workspace.ModeWrite}}}}
	s := base.WithDefaults()
	if s.CPUs != 2 || s.Memory != 2<<30 || s.Disk != 10<<30 {
		t.Fatalf("defaults = %+v", s)
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
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
		if err := b.Check(); !errors.Is(err, workspace.ErrInvalid) {
			t.Fatalf("ports %v: err = %v", ports, err)
		}
	}
	for name, b := range bad {
		if err := b.Check(); !errors.Is(err, workspace.ErrInvalid) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}
