// Package backendtest gives specs a backend build that behaves on cue: a
// run.sh that starts the test executable itself as a tiny gRPC backend that
// answers health checks, exits with output, or never listens.
//
// Importing the package is enough: its init turns the test executable into
// the backend when the run.sh asks for it through the environment, and does
// nothing otherwise. Only specs import it.
package backendtest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
)

// Mode is how the backend that a run.sh starts behaves.
type Mode string

const (
	// Serve listens on --addr and answers health checks.
	Serve Mode = "serve"
	// Exit writes a line to stderr and exits with code 3 at once.
	Exit Mode = "exit"
	// Hang never listens and never exits until it is stopped.
	Hang Mode = "hang"
)

// ExitMessage is the line a backend in mode Exit writes to stderr.
const ExitMessage = "fatal: libcuda.so.1: cannot open shared object file"

const modeEnv = "LOCALAI_BACKENDTEST_MODE"
const pidDirEnv = "LOCALAI_BACKENDTEST_PIDDIR"

func init() {
	mode := Mode(os.Getenv(modeEnv))
	if mode == "" {
		return
	}
	if dir := os.Getenv(pidDirEnv); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0o600)
	}
	switch mode {
	case Exit:
		fmt.Fprintln(os.Stderr, "loading backend")
		fmt.Fprintln(os.Stderr, ExitMessage)
		os.Exit(3)
	case Hang:
		for {
			time.Sleep(time.Hour)
		}
	case Serve:
		var addr string
		for i, arg := range os.Args {
			if arg == "--addr" && i+1 < len(os.Args) {
				addr = os.Args[i+1]
			}
		}
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			os.Exit(2)
		}
		srv := grpc.NewServer()
		pb.RegisterBackendServer(srv, healthy{})
		_ = srv.Serve(lis)
		os.Exit(0)
	}
	os.Exit(4)
}

type healthy struct{ pb.UnimplementedBackendServer }

func (healthy) Health(context.Context, *pb.HealthMessage) (*pb.Reply, error) {
	return &pb.Reply{Message: []byte("OK")}, nil
}

func (healthy) Free(context.Context, *pb.HealthMessage) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

// RunScript is a run.sh that starts the current executable as a backend in
// mode. When pidDir is not empty, the backend writes its PID there as an
// empty file named after it, so a spec can check that none is left running.
func RunScript(mode Mode, pidDir string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return "#!/bin/sh\n" +
		modeEnv + "=" + string(mode) + " " + pidDirEnv + "=" + strconv.Quote(pidDir) +
		" exec " + strconv.Quote(exe) + " \"$@\"\n", nil
}

// WriteBuild writes a backend build in dir: a run.sh in mode and a marker
// file named version, so a spec can tell builds apart.
func WriteBuild(dir string, mode Mode, version, pidDir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	script, err := RunScript(mode, pidDir)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o700); err != nil { // #nosec G306 -- the run.sh of a test backend must be executable
		return err
	}
	return os.WriteFile(filepath.Join(dir, "version"), []byte(version), 0o600)
}

// LivePIDs returns the PIDs recorded in pidDir whose process still runs.
func LivePIDs(pidDir string) []int {
	entries, err := os.ReadDir(pidDir)
	if err != nil {
		return nil
	}
	var live []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if processRuns(pid) {
			live = append(live, pid)
		}
	}
	return live
}
