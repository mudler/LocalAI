package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/model"

	"github.com/rs/zerolog/log"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

type BackendMonitor struct {
	configLoader *config.BackendConfigLoader
	modelLoader  *model.ModelLoader
	options      *config.ApplicationConfig // Taking options in case we need to inspect ExternalGRPCBackends, though that's out of scope for now, hence the name.
}

func NewBackendMonitor(configLoader *config.BackendConfigLoader, modelLoader *model.ModelLoader, appConfig *config.ApplicationConfig) BackendMonitor {
	return BackendMonitor{
		configLoader: configLoader,
		modelLoader:  modelLoader,
		options:      appConfig,
	}
}

func (bm BackendMonitor) getModelLoaderIDFromModelName(modelName string) (string, error) {
	config, exists := bm.configLoader.GetBackendConfig(modelName)
	var backendId string
	if exists {
		backendId = config.Model
	} else {
		// Last ditch effort: use it raw, see if a backend happens to match.
		backendId = modelName
	}

	if !strings.HasSuffix(backendId, ".bin") {
		backendId = fmt.Sprintf("%s.bin", backendId)
	}

	return backendId, nil
}

func (bm *BackendMonitor) SampleLocalBackendProcess(model string) (*schema.BackendMonitorResponse, error) {
	config, exists := bm.configLoader.GetBackendConfig(model)
	var backend string
	if exists {
		backend = config.Model
	} else {
		// Last ditch effort: use it raw, see if a backend happens to match.
		backend = model
	}

	if !strings.HasSuffix(backend, ".bin") {
		backend = fmt.Sprintf("%s.bin", backend)
	}

	pid, err := bm.modelLoader.GetGRPCPID(backend)

	if err != nil {
		log.Error().Err(err).Str("model", model).Msg("failed to find GRPC pid")
		return nil, err
	}

	// Name is slightly frightening but this does _not_ create a new process, rather it looks up an existing process by PID.
	backendProcess, err := gopsutil.NewProcess(int32(pid))

	if err != nil {
		log.Error().Err(err).Str("model", model).Int("pid", pid).Msg("error getting process info")
		return nil, err
	}

	memInfo, err := backendProcess.MemoryInfo()

	if err != nil {
		log.Error().Err(err).Str("model", model).Int("pid", pid).Msg("error getting memory info")
		return nil, err
	}

	memPercent, err := backendProcess.MemoryPercent()
	if err != nil {
		log.Error().Err(err).Str("model", model).Int("pid", pid).Msg("error getting memory percent")
		return nil, err
	}

	cpuPercent, err := backendProcess.CPUPercent()
	if err != nil {
		log.Error().Err(err).Str("model", model).Int("pid", pid).Msg("error getting cpu percent")
		return nil, err
	}

	return &schema.BackendMonitorResponse{
		MemoryInfo:    memInfo,
		MemoryPercent: memPercent,
		CPUPercent:    cpuPercent,
	}, nil
}

func (bm BackendMonitor) CheckAndSample(modelName string) (*proto.StatusResponse, error) {
	backendId, err := bm.getModelLoaderIDFromModelName(modelName)
	if err != nil {
		return nil, err
	}
	modelAddr := bm.modelLoader.CheckIsLoaded(backendId)
	if modelAddr == "" {
		return nil, fmt.Errorf("backend %s is not currently loaded", backendId)
	}

	status, rpcErr := modelAddr.GRPC(false, nil).Status(context.TODO())
	if rpcErr != nil {
		log.Warn().Msgf("backend %s experienced an error retrieving status info: %s", backendId, rpcErr.Error())
		val, slbErr := bm.SampleLocalBackendProcess(backendId)
		if slbErr != nil {
			return nil, fmt.Errorf("backend %s experienced an error retrieving status info via rpc: %s, then failed local node process sample: %s", backendId, rpcErr.Error(), slbErr.Error())
		}
		return &proto.StatusResponse{
			State: proto.StatusResponse_ERROR,
			Memory: &proto.MemoryUsageData{
				Total: val.MemoryInfo.VMS,
				Breakdown: map[string]uint64{
					"gopsutil-RSS": val.MemoryInfo.RSS,
				},
			},
		}, nil
	}
	return status, nil
}

func (bm BackendMonitor) ShutdownModel(modelName string) error {
	backendId, err := bm.getModelLoaderIDFromModelName(modelName)
	if err != nil {
		return err
	}
	return bm.modelLoader.ShutdownModel(backendId)
}
