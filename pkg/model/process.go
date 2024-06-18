package model

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hpcloud/tail"
	process "github.com/mudler/go-processmanager"
	"github.com/rs/zerolog/log"
)

func (ml *ModelLoader) StopAllExcept(s string) error {
	return ml.StopGRPC(func(id string, p *process.Process) bool {
		if id != s {
			for ml.models[id].GRPC(false, ml.wd).IsBusy() {
				log.Debug().Msgf("%s busy. Waiting.", id)
				time.Sleep(2 * time.Second)
			}
			log.Debug().Msgf("[single-backend] Stopping %s", id)
			return true
		}
		return false
	})
}

func (ml *ModelLoader) deleteProcess(s string) error {
	if _, exists := ml.grpcProcesses[s]; exists {
		if err := ml.grpcProcesses[s].Stop(); err != nil {
			return err
		}
	}
	delete(ml.grpcProcesses, s)
	delete(ml.models, s)
	return nil
}

type GRPCProcessFilter = func(id string, p *process.Process) bool

func includeAllProcesses(_ string, _ *process.Process) bool {
	return true
}

func (ml *ModelLoader) StopGRPC(filter GRPCProcessFilter) error {
	var err error = nil
	for k, p := range ml.grpcProcesses {
		if filter(k, p) {
			e := ml.deleteProcess(k)
			err = errors.Join(err, e)
		}
	}
	return err
}

func (ml *ModelLoader) StopAllGRPC() error {
	return ml.StopGRPC(includeAllProcesses)
}

func (ml *ModelLoader) GetGRPCPID(id string) (int, error) {
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

	grpcControlProcess := process.New(
		process.WithTemporaryStateDir(),
		process.WithName(grpcProcess),
		process.WithArgs(append(args, []string{"--addr", serverAddress}...)...),
		process.WithEnvironment(os.Environ()...),
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
		grpcControlProcess.Stop()
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
