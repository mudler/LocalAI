package localai

import (
	"fmt"
	"math"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// ForecastEndpoint forecasts the next steps of one or more numeric time series
// and returns quantile trajectories per series and variate.
// @Summary Forecasts numeric time series as quantile trajectories.
// @Tags forecast
// @Param request body schema.ForecastRequest true "query params"
// @Success 200 {object} schema.ForecastResponse "Response"
// @Router /v1/forecast [post]
func ForecastEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.ForecastRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		req, err := buildForecastRequest(input)
		if err != nil {
			return err
		}

		xlog.Debug("Forecast", "series", len(req.Series), "horizon", req.PredictionLength, "backend", cfg.Backend)

		res, err := backend.Forecast(c.Request().Context(), req, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		response, err := buildForecastResponse(input.Model, req, res)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, response)
	}
}

func forecastBadRequest(format string, args ...any) error {
	return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf(format, args...))
}

// forecastValue converts one JSON number to the float32 the backend takes. A
// nil value (JSON null) is a missing value and becomes NaN.
func forecastValue(v *float64, field string, row, step int) (float32, error) {
	if v == nil {
		return float32(math.NaN()), nil
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) || math.Abs(*v) > math.MaxFloat32 {
		return 0, forecastBadRequest("%s[%d][%d]: value %v is not a finite float32", field, row, step, *v)
	}
	return float32(*v), nil
}

// flattenForecastRows checks that every row holds steps values and returns the
// rows as one row-major slice: float32 for numeric rows, float64 for
// categorical rows, which must hold integer codes. The other slice is nil.
func flattenForecastRows(rows schema.ForecastRows, field string, steps int, categorical bool) ([]float32, []float64, error) {
	var f32 []float32
	var f64 []float64
	if categorical {
		f64 = make([]float64, 0, len(rows)*steps)
	} else {
		f32 = make([]float32, 0, len(rows)*steps)
	}
	for r, row := range rows {
		if len(row) != steps {
			return nil, nil, forecastBadRequest("%s[%d] has %d values, expected %d", field, r, len(row), steps)
		}
		for t, v := range row {
			if categorical {
				switch {
				case v == nil:
					f64 = append(f64, math.NaN())
				case math.IsNaN(*v) || math.IsInf(*v, 0) || *v != math.Trunc(*v):
					return nil, nil, forecastBadRequest("%s[%d][%d]: %v is not an integer code", field, r, t, *v)
				default:
					f64 = append(f64, *v)
				}
				continue
			}
			x, err := forecastValue(v, field, r, t)
			if err != nil {
				return nil, nil, err
			}
			f32 = append(f32, x)
		}
	}
	return f32, f64, nil
}

// buildForecastRequest validates the JSON request and packs it into the
// columnar proto message. Every problem the caller can fix is a 400.
func buildForecastRequest(in *schema.ForecastRequest) (*proto.ForecastRequest, error) {
	if in.PredictionLength < 1 {
		return nil, forecastBadRequest("prediction_length must be at least 1")
	}
	if in.MaxContext < 0 {
		return nil, forecastBadRequest("max_context must not be negative")
	}
	if len(in.Series) == 0 {
		return nil, forecastBadRequest("series must hold at least one series")
	}
	for _, q := range in.Quantiles {
		if math.IsNaN(q) || q <= 0 || q >= 1 {
			return nil, forecastBadRequest("quantiles must be between 0 and 1, exclusive")
		}
	}

	h := in.PredictionLength
	req := &proto.ForecastRequest{
		PredictionLength:   int32(h),
		Quantiles:          in.Quantiles,
		MaxContext:         int32(in.MaxContext),
		SymmetricAveraging: in.SymmetricAveraging,
		ClipNonnegative:    in.ClipNonnegative,
		Series:             make([]*proto.ForecastSeries, 0, len(in.Series)),
	}

	for i, s := range in.Series {
		name := fmt.Sprintf("series[%d]", i)
		if len(s.Target) == 0 {
			return nil, forecastBadRequest("%s.target must hold at least one variate", name)
		}
		t := len(s.Target[0])
		if t == 0 {
			return nil, forecastBadRequest("%s.target must hold at least one step", name)
		}
		target, _, err := flattenForecastRows(schema.ForecastRows(s.Target), name+".target", t, false)
		if err != nil {
			return nil, err
		}
		past, _, err := flattenForecastRows(s.PastCovariates, name+".past_covariates", t, false)
		if err != nil {
			return nil, err
		}
		known, _, err := flattenForecastRows(s.KnownCovariates, name+".known_covariates", t+h, false)
		if err != nil {
			return nil, err
		}
		_, catPast, err := flattenForecastRows(s.PastCategorical, name+".past_categorical", t, true)
		if err != nil {
			return nil, err
		}
		_, catKnown, err := flattenForecastRows(s.KnownCategorical, name+".known_categorical", t+h, true)
		if err != nil {
			return nil, err
		}
		req.Series = append(req.Series, &proto.ForecastSeries{
			Id:                  s.ID,
			NumVariates:         int32(len(s.Target)),
			Length:              int32(t),
			Target:              target,
			NumPastCovariates:   int32(len(s.PastCovariates)),
			PastCovariates:      past,
			NumKnownCovariates:  int32(len(s.KnownCovariates)),
			KnownCovariates:     known,
			NumPastCategorical:  int32(len(s.PastCategorical)),
			PastCategorical:     catPast,
			NumKnownCategorical: int32(len(s.KnownCategorical)),
			KnownCategorical:    catKnown,
		})
	}
	return req, nil
}

// buildForecastResponse reshapes the flat V*Q*H backend values into one
// [Q][H] block per variate. The backend answers in request order, so the
// series index is the position in the response.
func buildForecastResponse(modelName string, req *proto.ForecastRequest, res *proto.ForecastResponse) (*schema.ForecastResponse, error) {
	h := int(res.GetPredictionLength())
	levels := res.GetQuantileLevels()
	q := len(levels)
	if len(res.GetSeries()) != len(req.GetSeries()) {
		return nil, fmt.Errorf("forecast backend returned %d series for %d requested", len(res.GetSeries()), len(req.GetSeries()))
	}

	out := &schema.ForecastResponse{
		Object:           "forecast",
		Model:            modelName,
		PredictionLength: h,
		QuantileLevels:   levels,
		Data:             make([]schema.ForecastSeriesResult, 0, len(res.GetSeries())),
	}
	for i, sr := range res.GetSeries() {
		v := int(sr.GetNumVariates())
		vals := sr.GetValues()
		if v*q*h != len(vals) {
			return nil, fmt.Errorf("forecast backend returned %d values for series %d, expected %d", len(vals), i, v*q*h)
		}
		item := schema.ForecastSeriesResult{Index: i, ID: sr.GetId(), Variates: make([]schema.ForecastVariate, 0, v)}
		for vi := 0; vi < v; vi++ {
			rows := make([][]float32, q)
			for qi := 0; qi < q; qi++ {
				start := (vi*q + qi) * h
				rows[qi] = vals[start : start+h : start+h]
			}
			item.Variates = append(item.Variates, schema.ForecastVariate{Index: vi, Values: rows})
		}
		out.Data = append(out.Data, item)
	}
	return out, nil
}
