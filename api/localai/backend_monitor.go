package localai

import (
	config "github.com/go-skynet/LocalAI/api/config"

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

// TODO this code lives here temporarily. Should it live down in pkg/model/initializers instead?

func BackendMonitorEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(BackendMonitorRequest)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		pid, err := o.Loader.GetGRPCPID(input.Model)

		if err != nil {
			log.Error().Msgf("model %s : failed to find pid %+v", input.Model, err)
			return err
		}

		backendProcess, err := gopsutil.NewProcess(int32(pid))

		if err != nil {
			log.Error().Msgf("model %s [PID %d] : error getting process info %+v", input.Model, pid, err)
			return err
		}

		memInfo, err := backendProcess.MemoryInfo()

		if err != nil {
			log.Error().Msgf("model %s [PID %d] : error getting memory info %+v", input.Model, pid, err)
			return err
		}

		memPercent, err := backendProcess.MemoryPercent()
		if err != nil {
			log.Error().Msgf("model %s [PID %d] : error getting memory percent %+v", input.Model, pid, err)
			return err
		}

		cpuPercent, err := backendProcess.CPUPercent()
		if err != nil {
			log.Error().Msgf("model %s [PID %d] : error getting cpu percent %+v", input.Model, pid, err)
			return err
		}

		return c.JSON(BackendMonitorResponse{
			MemoryInfo:    memInfo,
			MemoryPercent: memPercent,
			CPUPercent:    cpuPercent,
		})
	}
}
