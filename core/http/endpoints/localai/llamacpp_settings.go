package localai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/llamacppsettings"
)

// maxLlamaCppBody caps the request body of the llama.cpp settings endpoints.
// The command itself is capped lower, by llamacppsettings.MaxCommandLength.
const maxLlamaCppBody = 64 * 1024

// LlamaCppParseCommandRequest is the body of POST /api/llama-cpp/parse-command.
type LlamaCppParseCommandRequest struct {
	// Command is a llama-server command line. It is tokenised, never run.
	Command string `json:"command"`
}

// LlamaCppCommandPreviewRequest is the body of POST /api/llama-cpp/command-preview.
type LlamaCppCommandPreviewRequest struct {
	// Config is a partial model config: the keys of the settings schema.
	Config llamacppsettings.Settings `json:"config"`
}

// LlamaCppSettingsSchemaResponse lists the supported llama-server flags.
type LlamaCppSettingsSchemaResponse struct {
	Flags     []llamacppsettings.Flag  `json:"flags"`
	Groups    []llamacppsettings.Group `json:"groups"`
	LoadModes []string                 `json:"load_modes"`
}

// LlamaCppPlacementRequest is the body of POST /api/llama-cpp/tensor-placement.
// Send either a placement to build an option, or an option to recognise.
type LlamaCppPlacementRequest struct {
	Placement *llamacppsettings.Placement `json:"placement,omitempty"`
	// Layers is an optional "0-6,18-23" string. It replaces placement.layers.
	Layers string `json:"layers,omitempty"`
	Option string `json:"option,omitempty"`
}

// LlamaCppPlacementResponse is a tensor placement and its "-ot" option.
type LlamaCppPlacementResponse struct {
	Option string `json:"option"`
	// Placement is null when the option is not one this server generates.
	Placement *llamacppsettings.Placement `json:"placement"`
	Raw       bool                        `json:"raw"`
}

func llamaCppError(c echo.Context, code int, msg string) error {
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{
		Code: code, Message: msg, Type: "invalid_request_error",
	}})
}

// decodeLlamaCppBody reads a JSON body with a size limit.
func decodeLlamaCppBody(c echo.Context, v any) error {
	body := http.MaxBytesReader(c.Response(), c.Request().Body, maxLlamaCppBody)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return llamaCppError(c, http.StatusRequestEntityTooLarge, "the request body is too large")
		}
		if errors.Is(err, io.EOF) {
			return llamaCppError(c, http.StatusBadRequest, "the request body is empty")
		}
		return llamaCppError(c, http.StatusBadRequest, "the request body is not valid JSON")
	}
	return nil
}

// LlamaCppParseCommandEndpoint turns a pasted llama-server command line into
// model config fields.
// @Summary Parse a llama-server command line
// @Description Reads a llama-server command and returns the model config keys and options it maps to. The command is only tokenised; it is never run. Flags without a first-class key are listed as unmapped and kept as raw passthrough options.
// @Tags config
// @Accept json
// @Produce json
// @Param request body LlamaCppParseCommandRequest true "The command line"
// @Success 200 {object} llamacppsettings.ParseResult "Parsed settings"
// @Failure 400 {object} schema.ErrorResponse "Invalid body or command"
// @Failure 413 {object} schema.ErrorResponse "Command too large"
// @Router /api/llama-cpp/parse-command [post]
func LlamaCppParseCommandEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		var req LlamaCppParseCommandRequest
		if err := decodeLlamaCppBody(c, &req); err != nil || c.Response().Committed {
			return err
		}
		if len(req.Command) > llamacppsettings.MaxCommandLength {
			return llamaCppError(c, http.StatusRequestEntityTooLarge, "the command is too long")
		}
		res, err := llamacppsettings.ParseCommand(req.Command)
		if err != nil {
			return llamaCppError(c, http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, res)
	}
}

// LlamaCppCommandPreviewEndpoint writes the llama-server command for a config.
// @Summary Preview the llama-server command of a model config
// @Description Builds the llama-server command line that matches a partial model config. The text is for display; nothing is run.
// @Tags config
// @Accept json
// @Produce json
// @Param request body LlamaCppCommandPreviewRequest true "Partial model config"
// @Success 200 {object} llamacppsettings.BuildResult "Command"
// @Failure 400 {object} schema.ErrorResponse "Invalid body"
// @Failure 413 {object} schema.ErrorResponse "Body too large"
// @Router /api/llama-cpp/command-preview [post]
func LlamaCppCommandPreviewEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		var req LlamaCppCommandPreviewRequest
		if err := decodeLlamaCppBody(c, &req); err != nil || c.Response().Committed {
			return err
		}
		res := llamacppsettings.BuildCommand(req.Config)
		return c.JSON(http.StatusOK, res)
	}
}

// LlamaCppSettingsSchemaEndpoint lists the supported llama-server flags.
// @Summary List the llama.cpp runtime settings
// @Description Returns the table of llama-server flags LocalAI maps to model config keys, with types, limits, defaults and a doc line, so a client can render the fields from one source.
// @Tags config
// @Produce json
// @Success 200 {object} LlamaCppSettingsSchemaResponse "Settings schema"
// @Router /api/llama-cpp/settings-schema [get]
func LlamaCppSettingsSchemaEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, LlamaCppSettingsSchemaResponse{
			Flags: llamacppsettings.Flags,
			Groups: []llamacppsettings.Group{
				llamacppsettings.GroupMemory, llamacppsettings.GroupSpeed, llamacppsettings.GroupSampling,
				llamacppsettings.GroupTemplate, llamacppsettings.GroupPlacement,
			},
			LoadModes: llamacppsettings.LoadModes(),
		})
	}
}

// LlamaCppTensorPlacementEndpoint builds or recognises a tensor placement.
// @Summary Build or recognise a llama.cpp tensor placement
// @Description With a placement, returns the "-ot" option that keeps the expert tensors of the chosen layers on the CPU. With an option, returns its structured form, or null when the pattern is not one this server generates. Placement is always opt-in: nothing applies it by default.
// @Tags config
// @Accept json
// @Produce json
// @Param request body LlamaCppPlacementRequest true "A placement or an option"
// @Success 200 {object} LlamaCppPlacementResponse "Placement"
// @Failure 400 {object} schema.ErrorResponse "Invalid input"
// @Failure 413 {object} schema.ErrorResponse "Body too large"
// @Router /api/llama-cpp/tensor-placement [post]
func LlamaCppTensorPlacementEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		var req LlamaCppPlacementRequest
		if err := decodeLlamaCppBody(c, &req); err != nil || c.Response().Committed {
			return err
		}
		switch {
		case req.Placement != nil && req.Option == "":
			p := *req.Placement
			if req.Layers != "" {
				rs, err := llamacppsettings.ParseLayerRanges(req.Layers)
				if err != nil {
					return llamaCppError(c, http.StatusBadRequest, err.Error())
				}
				p.Layers = rs
			}
			opt, err := p.Option()
			if err != nil {
				return llamaCppError(c, http.StatusBadRequest, err.Error())
			}
			// Report the form the server would recognise: ranges are merged.
			canon, _ := llamacppsettings.ParseOption(opt)
			return c.JSON(http.StatusOK, LlamaCppPlacementResponse{Option: opt, Placement: &canon})
		case req.Option != "" && req.Placement == nil:
			if len(req.Option) > llamacppsettings.MaxCommandLength {
				return llamaCppError(c, http.StatusRequestEntityTooLarge, "the option is too long")
			}
			p, ok := llamacppsettings.ParseOption(req.Option)
			if !ok {
				return c.JSON(http.StatusOK, LlamaCppPlacementResponse{Option: req.Option, Raw: true})
			}
			return c.JSON(http.StatusOK, LlamaCppPlacementResponse{Option: req.Option, Placement: &p})
		}
		return llamaCppError(c, http.StatusBadRequest, "send either placement or option")
	}
}
