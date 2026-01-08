package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hpcloud/tail"
	"github.com/mudler/LocalAI/pkg/signals"
	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
)

var forceBackendShutdown bool = os.Getenv("LOCALAI_FORCE_BACKEND_SHUTDOWN") == "true"

func (ml *ModelLoader) deleteProcess(s string) error {
	model, ok := ml.models[s]
	if !ok {
		xlog.Debug("Model not found", "model", s)
		return fmt.Errorf("model %s not found", s)
	}

	defer delete(ml.models, s)

	retries := 1
	for model.GRPC(false, ml.wd).IsBusy() {
		xlog.Debug("Model busy. Waiting.", "model", s)
		dur := time.Duration(retries*2) * time.Second
		if dur > retryTimeout {
			dur = retryTimeout
		}
		time.Sleep(dur)
		retries++

		if retries > 10 && forceBackendShutdown {
			xlog.Warn("Model is still busy after retries. Forcing shutdown.", "model", s, "retries", retries)
			break
		}
	}

	xlog.Debug("Deleting process", "model", s)

	process := model.Process()
	if process == nil {
		xlog.Error("No process", "model", s)
		// Nothing to do as there is no process
		return nil
	}

	err := process.Stop()
	if err != nil {
		xlog.Error("(deleteProcess) error while deleting process", "error", err, "model", s)
	}

	return err
}

func (ml *ModelLoader) StopGRPC(filter GRPCProcessFilter) error {
	var err error = nil
	ml.mu.Lock()
	defer ml.mu.Unlock()

	for k, m := range ml.models {
		if filter(k, m.Process()) {
			e := ml.deleteProcess(k)
			err = errors.Join(err, e)
		}
	}
	return err
}

func (ml *ModelLoader) StopAllGRPC() error {
	return ml.StopGRPC(all)
}

func (ml *ModelLoader) GetGRPCPID(id string) (int, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	p, exists := ml.models[id]
	if !exists {
		return -1, fmt.Errorf("no grpc backend found for %s", id)
	}
	if p.Process() == nil {
		return -1, fmt.Errorf("no grpc backend found for %s", id)
	}
	return strconv.Atoi(p.Process().PID)
}

func (ml *ModelLoader) startProcess(grpcProcess, id string, serverAddress string, args ...string) (*process.Process, error) {
	// Make sure the process is executable
	// Check first if it has executable permissions
	if fi, err := os.Stat(grpcProcess); err == nil {
		if fi.Mode()&0111 == 0 {
			xlog.Debug("Process is not executable. Making it executable.", "process", grpcProcess)
			if err := os.Chmod(grpcProcess, 0700); err != nil {
				return nil, err
			}
		}
	}

	xlog.Debug("Loading GRPC Process", "process", grpcProcess)

	xlog.Debug("GRPC Service will be running", "id", id, "address", serverAddress)

	workDir, err := filepath.Abs(filepath.Dir(grpcProcess))
	if err != nil {
		return nil, err
	}

	grpcControlProcess := process.New(
		process.WithTemporaryStateDir(),
		process.WithName(filepath.Base(grpcProcess)),
		process.WithArgs(append(args, []string{"--addr", serverAddress}...)...),
		process.WithEnvironment(os.Environ()...),
		process.WithWorkDir(workDir),
	)

	if ml.wd != nil {
		ml.wd.Add(serverAddress, grpcControlProcess)
		ml.wd.AddAddressModelMap(serverAddress, id)
	}

	if err := grpcControlProcess.Run(); err != nil {
		return grpcControlProcess, err
	}

	xlog.Debug("GRPC Service state dir", "dir", grpcControlProcess.StateDir())

	signals.RegisterGracefulTerminationHandler(func() {
		err := grpcControlProcess.Stop()
		if err != nil {
			xlog.Error("error while shutting down grpc process", "error", err)
		}
	})

	go func() {
		t, err := tail.TailFile(grpcControlProcess.StderrPath(), tail.Config{Follow: true})
		if err != nil {
			xlog.Debug("Could not tail stderr")
		}
		for line := range t.Lines {
			xlog.Debug("GRPC stderr", "id", strings.Join([]string{id, serverAddress}, "-"), "line", line.Text)
		}
	}()
	go func() {
		t, err := tail.TailFile(grpcControlProcess.StdoutPath(), tail.Config{Follow: true})
		if err != nil {
			xlog.Debug("Could not tail stdout")
		}
		for line := range t.Lines {
			xlog.Debug("GRPC stdout", "id", strings.Join([]string{id, serverAddress}, "-"), "line", line.Text)
		}
	}()

	return grpcControlProcess, nil
}
