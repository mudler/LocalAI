package model

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/hpcloud/tail"
	process "github.com/mudler/go-processmanager"
	"github.com/rs/zerolog/log"
)

func (ml *ModelLoader) deleteProcess(s string) error {
	if _, exists := ml.grpcProcesses[s]; exists {
		if err := ml.grpcProcesses[s].Stop(); err != nil {
			log.Error().Err(err).Msgf("(deleteProcess) error while deleting grpc process %s", s)
		}
	}
	delete(ml.grpcProcesses, s)
	delete(ml.models, s)
	return nil
}

func (ml *ModelLoader) StopGRPC(filter GRPCProcessFilter) error {
	var err error = nil
	for k, p := range ml.grpcProcesses {
		if filter(k, p) {
			e := ml.ShutdownModel(k)
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
	p, exists := ml.grpcProcesses[id]
	if !exists {
		return -1, fmt.Errorf("no grpc backend found for %s", id)
	}
	return strconv.Atoi(p.PID)
}

func (ml *ModelLoader) startProcess(grpcProcess, id string, serverAddress string, args ...string) error {
	// Make sure the process is executable
	if err := os.Chmod(grpcProcess, 0700); err != nil {
		return err
	}

	log.Debug().Msgf("Loading GRPC Process: %s", grpcProcess)

	log.Debug().Msgf("GRPC Service for %s will be running at: '%s'", id, serverAddress)

	workDir, err := filepath.Abs(filepath.Dir(grpcProcess))
	if err != nil {
		return err
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

	ml.grpcProcesses[id] = grpcControlProcess

	if err := grpcControlProcess.Run(); err != nil {
		return err
	}

	log.Debug().Msgf("GRPC Service state dir: %s", grpcControlProcess.StateDir())
	// clean up process
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		err := grpcControlProcess.Stop()
		if err != nil {
			log.Error().Err(err).Msg("error while shutting down grpc process")
		}
	}()

	go func() {
		t, err := tail.TailFile(grpcControlProcess.StderrPath(), tail.Config{Follow: true})
		if err != nil {
			log.Debug().Msgf("Could not tail stderr")
		}
		for line := range t.Lines {
			log.Debug().Msgf("GRPC(%s): stderr %s", strings.Join([]string{id, serverAddress}, "-"), line.Text)
		}
	}()
	go func() {
		t, err := tail.TailFile(grpcControlProcess.StdoutPath(), tail.Config{Follow: true})
		if err != nil {
			log.Debug().Msgf("Could not tail stdout")
		}
		for line := range t.Lines {
			log.Debug().Msgf("GRPC(%s): stdout %s", strings.Join([]string{id, serverAddress}, "-"), line.Text)
		}
	}()

	return nil
}
