package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/rs/zerolog/log"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

type BackendMonitorService struct {
	backendConfigLoader *config.BackendConfigLoader
	modelLoader         *model.ModelLoader
	options             *config.ApplicationConfig // Taking options in case we need to inspect ExternalGRPCBackends, though that's out of scope for now, hence the name.
}

func NewBackendMonitorService(modelLoader *model.ModelLoader, configLoader *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *BackendMonitorService {
	return &BackendMonitorService{
		modelLoader:         modelLoader,
		backendConfigLoader: configLoader,
		options:             appConfig,
	}
}

func (bms BackendMonitorService) getModelLoaderIDFromModelName(modelName string) (string, error) {
	config, exists := bms.backendConfigLoader.GetBackendConfig(modelName)
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

func (bms *BackendMonitorService) SampleLocalBackendProcess(model string) (*schema.BackendMonitorResponse, error) {
	config, exists := bms.backendConfigLoader.GetBackendConfig(model)
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

	pid, err := bms.modelLoader.GetGRPCPID(backend)

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

func (bms BackendMonitorService) CheckAndSample(modelName string) (*proto.StatusResponse, error) {
	backendId, err := bms.getModelLoaderIDFromModelName(modelName)
	if err != nil {
		return nil, err
	}
	modelAddr := bms.modelLoader.CheckIsLoaded(backendId)
	if modelAddr == nil {
		return nil, fmt.Errorf("backend %s is not currently loaded", backendId)
	}

	status, rpcErr := modelAddr.GRPC(false, nil).Status(context.TODO())
	if rpcErr != nil {
		log.Warn().Msgf("backend %s experienced an error retrieving status info: %s", backendId, rpcErr.Error())
		val, slbErr := bms.SampleLocalBackendProcess(backendId)
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

func (bms BackendMonitorService) ShutdownModel(modelName string) error {
	backendId, err := bms.getModelLoaderIDFromModelName(modelName)
	if err != nil {
		return err
	}
	return bms.modelLoader.ShutdownModel(backendId)
}
