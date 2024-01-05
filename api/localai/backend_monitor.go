package localai

import (
	"context"
	"fmt"
	"strings"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"

	"github.com/go-skynet/LocalAI/api/options"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

type BackendMonitorRequest struct {
	Model string `json:"model" yaml:"model"`
}

type BackendMonitorResponse struct {
	MemoryInfo    *gopsutil.MemoryInfoStat
	MemoryPercent float32
	CPUPercent    float64
}

type BackendMonitor struct {
	configLoader *config.ConfigLoader
	options      *options.Option // Taking options in case we need to inspect ExternalGRPCBackends, though that's out of scope for now, hence the name.
}

func NewBackendMonitor(configLoader *config.ConfigLoader, options *options.Option) BackendMonitor {
	return BackendMonitor{
		configLoader: configLoader,
		options:      options,
	}
}

func (bm *BackendMonitor) SampleLocalBackendProcess(model string) (*BackendMonitorResponse, error) {
	config, exists := bm.configLoader.GetConfig(model)
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

	pid, err := bm.options.Loader.GetGRPCPID(backend)

	if err != nil {
		log.Error().Msgf("model %s : failed to find pid %+v", model, err)
		return nil, err
	}

	// Name is slightly frightening but this does _not_ create a new process, rather it looks up an existing process by PID.
	backendProcess, err := gopsutil.NewProcess(int32(pid))

	if err != nil {
		log.Error().Msgf("model %s [PID %d] : error getting process info %+v", model, pid, err)
		return nil, err
	}

	memInfo, err := backendProcess.MemoryInfo()

	if err != nil {
		log.Error().Msgf("model %s [PID %d] : error getting memory info %+v", model, pid, err)
		return nil, err
	}

	memPercent, err := backendProcess.MemoryPercent()
	if err != nil {
		log.Error().Msgf("model %s [PID %d] : error getting memory percent %+v", model, pid, err)
		return nil, err
	}

	cpuPercent, err := backendProcess.CPUPercent()
	if err != nil {
		log.Error().Msgf("model %s [PID %d] : error getting cpu percent %+v", model, pid, err)
		return nil, err
	}

	return &BackendMonitorResponse{
		MemoryInfo:    memInfo,
		MemoryPercent: memPercent,
		CPUPercent:    cpuPercent,
	}, nil
}

func (bm BackendMonitor) getModelLoaderIDFromCtx(c *fiber.Ctx) (string, error) {
	input := new(BackendMonitorRequest)
	// Get input data from the request body
	if err := c.BodyParser(input); err != nil {
		return "", err
	}

	config, exists := bm.configLoader.GetConfig(input.Model)
	var backendId string
	if exists {
		backendId = config.Model
	} else {
		// Last ditch effort: use it raw, see if a backend happens to match.
		backendId = input.Model
	}

	if !strings.HasSuffix(backendId, ".bin") {
		backendId = fmt.Sprintf("%s.bin", backendId)
	}

	return backendId, nil
}

func BackendMonitorEndpoint(bm BackendMonitor) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		backendId, err := bm.getModelLoaderIDFromCtx(c)
		if err != nil {
			return err
		}

		model := bm.options.Loader.CheckIsLoaded(backendId)
		if model == "" {
			return fmt.Errorf("backend %s is not currently loaded", backendId)
		}

		status, rpcErr := model.GRPC(false, nil).Status(context.TODO())
		if rpcErr != nil {
			log.Warn().Msgf("backend %s experienced an error retrieving status info: %s", backendId, rpcErr.Error())
			val, slbErr := bm.SampleLocalBackendProcess(backendId)
			if slbErr != nil {
				return fmt.Errorf("backend %s experienced an error retrieving status info via rpc: %s, then failed local node process sample: %s", backendId, rpcErr.Error(), slbErr.Error())
			}
			return c.JSON(proto.StatusResponse{
				State: proto.StatusResponse_ERROR,
				Memory: &proto.MemoryUsageData{
					Total: val.MemoryInfo.VMS,
					Breakdown: map[string]uint64{
						"gopsutil-RSS": val.MemoryInfo.RSS,
					},
				},
			})
		}

		return c.JSON(status)
	}
}

func BackendShutdownEndpoint(bm BackendMonitor) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		backendId, err := bm.getModelLoaderIDFromCtx(c)
		if err != nil {
			return err
		}

		return bm.options.Loader.ShutdownModel(backendId)
	}
}
