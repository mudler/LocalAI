package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/mudler/xlog"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

type BackendMonitorService struct {
	modelConfigLoader *config.ModelConfigLoader
	modelLoader       *model.ModelLoader
	options           *config.ApplicationConfig // Taking options in case we need to inspect ExternalGRPCBackends, though that's out of scope for now, hence the name.
}

func NewBackendMonitorService(modelLoader *model.ModelLoader, configLoader *config.ModelConfigLoader, appConfig *config.ApplicationConfig) *BackendMonitorService {
	return &BackendMonitorService{
		modelLoader:       modelLoader,
		modelConfigLoader: configLoader,
		options:           appConfig,
	}
}

func (bms *BackendMonitorService) SampleLocalBackendProcess(model string) (*schema.BackendMonitorResponse, error) {
	config, exists := bms.modelConfigLoader.GetModelConfig(model)
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
		xlog.Error("failed to find GRPC pid", "error", err, "model", model)
		return nil, err
	}

	// Name is slightly frightening but this does _not_ create a new process, rather it looks up an existing process by PID.
	backendProcess, err := gopsutil.NewProcess(int32(pid))

	if err != nil {
		xlog.Error("error getting process info", "error", err, "model", model, "pid", pid)
		return nil, err
	}

	memInfo, err := backendProcess.MemoryInfo()

	if err != nil {
		xlog.Error("error getting memory info", "error", err, "model", model, "pid", pid)
		return nil, err
	}

	memPercent, err := backendProcess.MemoryPercent()
	if err != nil {
		xlog.Error("error getting memory percent", "error", err, "model", model, "pid", pid)
		return nil, err
	}

	cpuPercent, err := backendProcess.CPUPercent()
	if err != nil {
		xlog.Error("error getting cpu percent", "error", err, "model", model, "pid", pid)
		return nil, err
	}

	return &schema.BackendMonitorResponse{
		MemoryInfo:    memInfo,
		MemoryPercent: memPercent,
		CPUPercent:    cpuPercent,
	}, nil
}

func (bms BackendMonitorService) CheckAndSample(modelName string) (*proto.StatusResponse, error) {
	modelAddr := bms.modelLoader.CheckIsLoaded(modelName)
	if modelAddr == nil {
		return nil, fmt.Errorf("backend %s is not currently loaded", modelName)
	}

	status, rpcErr := modelAddr.GRPC(false, nil).Status(context.TODO())
	if rpcErr != nil {
		xlog.Warn("backend experienced an error retrieving status info", "backend", modelName, "error", rpcErr)
		val, slbErr := bms.SampleLocalBackendProcess(modelName)
		if slbErr != nil {
			return nil, fmt.Errorf("backend %s experienced an error retrieving status info via rpc: %s, then failed local node process sample: %s", modelName, rpcErr.Error(), slbErr.Error())
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
	return bms.modelLoader.ShutdownModel(modelName)
}
