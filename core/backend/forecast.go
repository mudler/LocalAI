package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

// Forecast runs a time-series forecasting model on the series in the request
// and returns quantile trajectories for the next prediction_length steps.
func Forecast(
	ctx context.Context,
	in *proto.ForecastRequest,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (*proto.ForecastResponse, error) {
	opts := ModelOptions(modelConfig, appConfig)
	forecastModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	if forecastModel == nil {
		return nil, fmt.Errorf("could not load forecast model")
	}
	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return nil, err
	}
	defer release()

	summary := fmt.Sprintf("%d series, horizon %d", len(in.GetSeries()), in.GetPredictionLength())

	var startTime time.Time
	var traceID string
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
		traceID = trace.BeginBackendTrace(trace.BackendTrace{Timestamp: startTime, Type: trace.BackendTraceForecast, ModelName: modelConfig.Name, Backend: modelConfig.Backend, Summary: summary})
	}
	defer trace.CancelBackendTrace(traceID)

	// Stamped here for the same reason as in rerank.go: the caller builds the
	// request without a ModelConfig, this function has the one that loaded.
	in.ModelIdentity = modelConfig.Model

	res, err := forecastModel.Forecast(ctx, in)

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}

		trace.RecordBackendTrace(trace.BackendTrace{
			ID:        traceID,
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceForecast,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   summary,
			Error:     errStr,
			Data: map[string]any{
				"series":            len(in.GetSeries()),
				"prediction_length": in.GetPredictionLength(),
				"quantiles":         in.GetQuantiles(),
			},
		})
	}

	return res, err
}
