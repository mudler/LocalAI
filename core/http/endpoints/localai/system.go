package localai

import (
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/monitoring"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

// SystemInformations returns the system informations
// @Summary Show the LocalAI instance information
// @Tags monitoring
// @Success 200 {object} schema.SystemInformationResponse "Response"
// @Router /system [get]
func SystemInformations(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, sampler *monitoring.LocalProcessSampler) echo.HandlerFunc {
	return func(c echo.Context) error {
		availableBackends := []string{}
		loadedModels := ml.ListLoadedModels()
		for b := range appConfig.ExternalGRPCBackends {
			availableBackends = append(availableBackends, b)
		}
		for b := range ml.GetAllExternalBackends(nil) {
			availableBackends = append(availableBackends, b)
		}

		sysmodels := []schema.SysInfoModel{}
		live := map[int32]struct{}{}
		for _, m := range loadedModels {
			entry := schema.SysInfoModel{ID: m.ID}
			// The loader tracks only the ID. Which engine is serving a model is
			// the first thing an operator wants beside its name, and it is one
			// config lookup away.
			if cfg, ok := cl.GetModelConfig(m.ID); ok {
				entry.Backend = cfg.Backend
			}
			if pid, ok := localPID(m); ok && sampler != nil {
				live[pid] = struct{}{}
				if proc, err := sampler.Sample(pid); err == nil {
					entry.Process = proc
				}
			}
			if pid, ok := localPID(m); ok {
				if used, ok := xsysinfo.ProcessVRAM(int(pid)); ok {
					entry.SizeVRAM = &used
				}
			}
			sysmodels = append(sysmodels, entry)
		}
		if sampler != nil {
			sampler.Retain(live)
		}
		return c.JSON(200,
			schema.SystemInformationResponse{
				Backends: availableBackends,
				Models:   sysmodels,
			},
		)
	}
}

// localPID is the PID of the backend process this host started for m. A model
// served by a remote worker, or through an external gRPC address, has none.
func localPID(m *model.Model) (int32, bool) {
	p := m.Process()
	if p == nil {
		return 0, false
	}
	pid, err := strconv.ParseInt(p.CurrentPID(), 10, 32)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return int32(pid), true
}
