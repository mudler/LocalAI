package localai

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery/remotemodel"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

// maxEstimateContexts bounds the contexts parameter.
const maxEstimateContexts = 8

func remoteError(c echo.Context, err error) error {
	status, kind := http.StatusBadGateway, "api_error"
	switch {
	case errors.Is(err, modelsearch.ErrBadRepo), errors.Is(err, modelsearch.ErrNoSuchFile):
		status, kind = http.StatusBadRequest, "invalid_request_error"
	case errors.Is(err, modelsearch.ErrRepoNotFound):
		status, kind = http.StatusNotFound, "not_found_error"
	case errors.Is(err, modelsearch.ErrRateLimited):
		status, kind = http.StatusTooManyRequests, "rate_limit_error"
		c.Response().Header().Set("Retry-After", "60")
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	}
	return c.JSON(status, schema.ErrorResponse{
		Error: &schema.APIError{Message: err.Error(), Code: status, Type: kind},
	})
}

func remoteBadRequest(c echo.Context, msg string) error {
	return c.JSON(http.StatusBadRequest, schema.ErrorResponse{
		Error: &schema.APIError{Message: msg, Code: http.StatusBadRequest, Type: "invalid_request_error"},
	})
}

// installedIndex indexes the installed model configs. A nil loader (a test,
// or a server with none) gives an index that finds nothing.
func installedIndex(cl *config.ModelConfigLoader) *remotemodel.InstalledIndex {
	if cl == nil {
		return remotemodel.NewInstalledIndex(nil)
	}
	return remotemodel.NewInstalledIndex(cl.GetAllModelsConfigs())
}

// RemoteModelFilesEndpoint lists what a remote repository offers.
//
//	@Summary	List the weight files of a remote model, grouped by quantization
//	@Description	Reads one repository of Hugging Face or Pirate Face and returns its weight files with sizes, one variant per quantization (shards are one variant), the licence, whether access is gated, a short description from the model card, the GGUF header summary Hugging Face reports, whether an installed model already comes from the repository, and the backend a dry run of the import selects. Nothing is downloaded or enqueued. A variant's `preference` is the value for `preferences.quantizations` on POST /models/import-uri that selects exactly that variant. Sizes of a Pirate Face repository come from the Hugging Face commit its torrent mirrors; `size_known` is false when Hugging Face does not know the repository. The answer is cached per repository for 10 minutes.
//	@Tags		models
//	@Param		uri				query	string	true	"Repository uri: huggingface://owner/repo, pirateface://owner/repo or a huggingface.co url"
//	@Param		quantization	query	string	false	"Quantization preference to dry-run the import with (default: the importer's)"
//	@Success	200	{object}	modelsearch.RepoFiles
//	@Failure	400	{object}	schema.ErrorResponse
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	429	{object}	schema.ErrorResponse
//	@Failure	502	{object}	schema.ErrorResponse
//	@Router		/api/models/remote/files [get]
func RemoteModelFilesEndpoint(in *modelsearch.Inspector, cl *config.ModelConfigLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		uri := strings.TrimSpace(c.QueryParam("uri"))
		if uri == "" {
			return remoteBadRequest(c, "uri is required")
		}
		rf, err := in.Files(c.Request().Context(), uri)
		if err != nil {
			return remoteError(c, err)
		}
		return c.JSON(http.StatusOK, remotemodel.PreviewFiles(rf, installedIndex(cl), strings.TrimSpace(c.QueryParam("quantization"))))
	}
}

// RemoteModelEstimateEndpoint estimates the memory a remote model needs.
//
//	@Summary	Estimate the memory a remote model file needs
//	@Description	Same answer as GET /api/models/estimate/{id} for a gallery model, for one quantization of a Hugging Face or Pirate Face repository: the download size and the memory needed at each requested context size, plus `file`, `quantization` and `source`. `source` is `gguf-header` when the layer count, width and heads were read from the GGUF header (exact KV cache term), `file-size` when only the file size was known, and `unknown` when no size was known. `approximate` is true whenever the architecture was not read, so the figure is the file size plus a rule of thumb per context token. Hugging Face has no range-read for gated repositories: the estimate then falls back to file sizes. Estimates run through the same bounded upstream budget as the file listing.
//	@Tags		models
//	@Param		uri			query	string	true	"Repository uri: huggingface://owner/repo or pirateface://owner/repo"
//	@Param		file		query	string	false	"First file of a variant, as listed by /api/models/remote/files (default: the recommended variant)"
//	@Param		contexts	query	string	false	"Comma separated context sizes (default 8192)"
//	@Success	200	{object}	modelsearch.EstimateResult
//	@Failure	400	{object}	schema.ErrorResponse
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	429	{object}	schema.ErrorResponse
//	@Failure	502	{object}	schema.ErrorResponse
//	@Router		/api/models/remote/estimate [get]
func RemoteModelEstimateEndpoint(in *modelsearch.Inspector) echo.HandlerFunc {
	return func(c echo.Context) error {
		uri := strings.TrimSpace(c.QueryParam("uri"))
		if uri == "" {
			return remoteBadRequest(c, "uri is required")
		}
		contexts, err := parseEstimateContexts(c.QueryParam("contexts"))
		if err != nil {
			return remoteBadRequest(c, err.Error())
		}
		res, err := in.Estimate(c.Request().Context(), uri, strings.TrimSpace(c.QueryParam("file")), contexts)
		if err != nil {
			return remoteError(c, err)
		}
		return c.JSON(http.StatusOK, res)
	}
}

func parseEstimateContexts(raw string) ([]uint32, error) {
	if strings.TrimSpace(raw) == "" {
		return []uint32{config.DefaultEstimateContextSize}, nil
	}
	var out []uint32
	for _, s := range strings.Split(raw, ",") {
		n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
		if err != nil || n < 128 || n > 4<<20 {
			return nil, errors.New("contexts must be comma separated sizes between 128 and 4194304")
		}
		out = append(out, uint32(n))
	}
	if len(out) > maxEstimateContexts {
		return nil, errors.New("at most " + strconv.Itoa(maxEstimateContexts) + " contexts")
	}
	return out, nil
}
