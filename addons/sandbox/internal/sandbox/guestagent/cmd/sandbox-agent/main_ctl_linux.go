package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
)

// ctlDir is the part of guestagent.CtlDir that serveCtl uses.
type ctlDir interface {
	Hide() error
	Listen(name string) (net.Listener, error)
	Dial(name string) (net.Conn, error)
}

// runCtl is the agent under gVisor. The host binds the lower tree at
// /mnt/lower, the upper folder at /mnt/upper and the control folder ctl into
// the sandbox. The agent hides ctl from every other process, then listens on
// ctl/agent.sock and forwards the ports in SANDBOX_AGENT_PORTS to
// ctl/port-<n>.sock. It never returns.
func runCtl(ctl string) {
	if err := guestagent.Boot(guestagent.RealSys{}, guestagent.BootConfig{
		Uid: agentUID, Gid: agentGID,
		LowerBind: "/mnt/lower", UpperBind: "/mnt/upper", PlainOverlay: true,
	}); err != nil {
		failCtl("boot: %v", err)
	}
	// The sandbox gives the init process its capabilities as ambient. A
	// command must not inherit them: the spawner raises its own two.
	if err := guestagent.ClearAmbient(); err != nil {
		failCtl("clear ambient capabilities: %v", err)
	}
	dir, err := guestagent.OpenCtlDir(ctl)
	if err != nil {
		failCtl("%v", err)
	}
	sl, srv, err := serveCtl(dir, guestagent.ParsePortList(os.Getenv(guestagent.EnvPorts))) //nolint:forbidigo // guest agent entry point; the host passes its settings through the environment
	if err != nil {
		failCtl("%v", err)
	}
	// SIGUSR1: the host restored the sandbox from a checkpoint, which needed
	// the listener closed. Listen again (through the descriptor of ctl, which
	// stays valid after Hide).
	usr := make(chan os.Signal, 4)
	signal.Notify(usr, syscall.SIGUSR1)
	for range usr {
		if err := retry(listenTries, listenWait, func() error { return sl.Start(srv) }); err != nil {
			log.Printf("listen again: %v", err)
		}
	}
}

// The listener of the agent is the only way in, so a failed listen after a
// restore is tried a few times before it is given up.
const (
	listenTries = 5
	listenWait  = 100 * time.Millisecond
)

// retry calls f up to tries times, waiting wait between the calls, and
// returns nil at the first success or the last error.
func retry(tries int, wait time.Duration, f func() error) (err error) {
	for i := range tries {
		if i > 0 {
			time.Sleep(wait)
		}
		if err = f(); err == nil {
			return nil
		}
	}
	return err
}

// serveCtl hides the control folder, then opens the sockets in it and serves.
// The order is a security property: the first request the agent accepts can
// start a command, and that command must not find the sockets or the
// descriptors of the agent. When Hide fails, nothing listens.
func serveCtl(dir ctlDir, ports []int) (*guestagent.SwitchListener, *guestagent.Server, error) {
	if err := dir.Hide(); err != nil {
		return nil, nil, fmt.Errorf("hide the control folder: %w", err)
	}
	sl := &guestagent.SwitchListener{Listen: func() (net.Listener, error) { return dir.Listen("agent.sock") }}
	srv := &guestagent.Server{
		Spawner: newSpawner(),
		// Guest writes reach the host files at once, so there is nothing to
		// flush: the host pauses the sandbox instead of freezing a disk.
		Checkpoint: sl.Stop,
		Shutdown: func() error {
			go func() {
				time.Sleep(100 * time.Millisecond)
				syscall.Sync()
				os.Exit(0)
			}()
			return nil
		},
	}
	for _, port := range ports {
		tl, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
		if err != nil {
			return nil, nil, fmt.Errorf("listen on port %d: %w", port, err)
		}
		name := fmt.Sprintf("port-%d.sock", port)
		go func() { _ = guestagent.Forward(tl, func() (net.Conn, error) { return dir.Dial(name) }) }()
	}
	if err := sl.Start(srv); err != nil {
		return nil, nil, fmt.Errorf("listen: %w", err)
	}
	return sl, srv, nil
}

// failCtl logs the error and ends the sandbox. The host reads the log.
func failCtl(format string, args ...any) {
	log.Printf(format, args...)
	syscall.Sync()
	os.Exit(1)
}
