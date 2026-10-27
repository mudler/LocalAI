// Command sandbox-agent is the guest agent. It runs as PID 1 (init=) in a
// sandbox guest.
package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
)

const agentUID, agentGID = 1000, 1000

func main() {
	log.SetFlags(0)
	log.SetPrefix("sandbox-agent: ")
	ignoreSignals()
	// No setuid binary in the image may regain root: the processes keep the
	// two ambient capabilities and nothing more.
	if err := setNoNewPrivs(); err != nil {
		fail("no_new_privs: %v", err)
	}
	if ctl := os.Getenv(guestagent.EnvCtl); ctl != "" { //nolint:forbidigo // guest agent entry point; the host passes its settings through the environment
		runCtl(ctl) // gVisor: no vsock, no PID 1 power-off; never returns
	}
	if err := guestagent.Boot(guestagent.RealSys{}, guestagent.BootConfig{Uid: agentUID, Gid: agentGID}); err != nil {
		fail("boot: %v", err)
	}
	cmdline, _ := os.ReadFile("/proc/cmdline")
	fr := &guestagent.Freezer{Path: "/run/upper"}
	srv := &guestagent.Server{
		Spawner: newSpawner(),
		Freeze:  fr.Freeze,
		Thaw:    fr.Thaw,
		Shutdown: func() error {
			go func() {
				time.Sleep(100 * time.Millisecond)
				if !fr.Frozen() { // a sync of a frozen file system blocks
					syscall.Sync()
				}
				_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
			}()
			return nil
		},
	}
	l, err := guestagent.ListenVsock(guestagent.AgentPort)
	if err != nil {
		fail("listen on vsock: %v", err)
	}
	for _, port := range guestagent.ParsePorts(string(cmdline)) {
		tl, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
		if err != nil {
			fail("listen on port %d: %v", port, err)
		}
		go func() {
			_ = guestagent.Forward(tl, func() (net.Conn, error) { return guestagent.DialVsock(guestagent.HostCID, uint32(port)) })
		}()
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("serve: recovered panic: %v", r)
			}
		}()
		_ = srv.Serve(guestagent.RetryListener{Listener: l})
	}()
	sendReady()
	select {}
}

// newSpawner returns the Spawner of the agent: commands run as the agent
// user with only CAP_DAC_OVERRIDE and CAP_FOWNER as ambient capabilities
// (the lower layer holds read-only blobs).
func newSpawner() *guestagent.LinuxSpawner {
	return &guestagent.LinuxSpawner{
		Credential:  &syscall.Credential{Uid: agentUID, Gid: agentGID, Groups: []uint32{agentGID}},
		AmbientCaps: []uintptr{guestagent.CapDACOverride, guestagent.CapFowner},
		Env: []string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "SHELL=/bin/bash", "LANG=C.UTF-8",
		},
		Dir:  "/workspace",
		Reap: true,
	}
}

func sendReady() {
	var err error
	for range 100 {
		var c net.Conn
		if c, err = guestagent.DialVsock(guestagent.HostCID, guestagent.AgentPort); err == nil {
			err = guestagent.SendReady(c)
			_ = c.Close()
			if err == nil {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	fail("send ready: %v", err)
}

// powerOff powers the guest off. It returns only when the kernel refuses.
func powerOff() error { return syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF) }

// fail logs the error to the console, then powers the guest off. The
// console keeps the message for the host to read.
func fail(format string, args ...any) {
	halt(log.Printf, time.Sleep, syscall.Sync, powerOff, format, args...)
}

// halt logs, pauses so the console can drain, syncs the disks, powers off,
// and holds PID 1 when the power-off returns. Holding matters: PID 1 must
// never exit, or the kernel panics.
func halt(logf func(string, ...any), pause func(time.Duration), sync func(), poweroff func() error, format string, args ...any) {
	logf(format, args...)
	pause(2 * time.Second)
	sync()
	_ = poweroff()
	for {
		pause(time.Hour)
	}
}

// setNoNewPrivs sets PR_SET_NO_NEW_PRIVS, which execve honours: a setuid or
// file-capability binary no longer gains privilege, and the ambient
// capabilities survive the exec. The flag is per thread, and a child forks
// from whichever thread runs its spawn, so the call goes to every thread.
// AllThreadsSyscall6 needs a binary without cgo, which the agent is.
func setNoNewPrivs() error {
	const prSetNoNewPrivs = 38
	if _, _, e := syscall.AllThreadsSyscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0); e != 0 {
		return e
	}
	return nil
}

// ignoreSignals drains SIGTERM, SIGINT and SIGHUP. The Go runtime's default
// handler would exit PID 1, and the kernel would panic.
func ignoreSignals() {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("signals: recovered panic: %v", r)
			}
		}()
		for s := range ch {
			log.Printf("ignoring signal %v", s)
		}
	}()
}

func itoa(n int) string { return strconv.Itoa(n) }
