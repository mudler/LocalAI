//go:build linux || darwin

package host_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/host"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func TestTheHostBackendPassesTheConformanceSuite(t *testing.T) {
	sbtest.RunBackendSuite(t, func(t *testing.T) sandbox.Backend {
		b, err := host.New(host.Options{Root: t.TempDir()})
		if err != nil {
			t.Skipf("the host backend is not available here: %v", err)
		}
		return b
	})
}

func newSandbox(t *testing.T, b sandbox.Backend) sandbox.Sandbox {
	t.Helper()
	st := sbtest.OpenStore(t)
	ctx := context.Background()
	if _, err := st.Mkdir(ctx, sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	if err := st.Grant(ctx, sbtest.Admin, sbtest.Agent.Principal, "/proj", workspace.ModeWrite); err != nil {
		t.Fatal(err)
	}
	v, err := st.View(ctx, sbtest.Agent, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Agent}, View: v})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb
}

func TestEveryCommandIsLoggedWithTheUser(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	b, err := host.New(host.Options{Root: t.TempDir(), Log: func(who, cmd string) {
		mu.Lock()
		lines = append(lines, who+": "+cmd)
		mu.Unlock()
	}})
	if err != nil {
		t.Skip(err)
	}
	sb := newSandbox(t, b)
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "echo hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], sbtest.Agent.Principal+": sh -c echo hi") {
		t.Fatalf("log = %v", lines)
	}
}

func TestACommandSeesNothingOfTheAddonEnvironment(t *testing.T) {
	t.Setenv("LOCALAI_TEST_SECRET", "do-not-leak")
	b, err := host.New(host.Options{Root: t.TempDir()})
	if err != nil {
		t.Skip(err)
	}
	sb := newSandbox(t, b)
	var out strings.Builder
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "env"}, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "do-not-leak") || strings.Contains(out.String(), "LOCALAI_TEST_SECRET") {
		t.Fatalf("the command saw the environment of the add-on:\n%s", out.String())
	}
}

func TestLeftoversOfAnEarlierRunAreRemoved(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(root+"/sb-old/work", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := host.New(host.Options{Root: root}); err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(root + "/sb-old"); !os.IsNotExist(err) {
		t.Errorf("the leftover is still there: %v", err)
	}
}

func TestARelativeRootIsRefused(t *testing.T) {
	if _, err := host.New(host.Options{Root: "relative/dir"}); err == nil {
		t.Error("a relative root was accepted")
	}
	_ = os.Getenv
	_ = errors.New
}
