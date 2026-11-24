package localai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/enginesettings"
)

// maxEngineBody caps the request body of the engine settings endpoints. The
// command itself is capped lower, by enginesettings.MaxCommandLength.
const maxEngineBody = 64 * 1024

// EngineListResponse lists the engines whose runtime settings LocalAI maps.
type EngineListResponse struct {
	Engines []enginesettings.Info `json:"engines"`
}

// EngineParseCommandRequest is the body of POST /api/engines/{engine}/parse-command.
type EngineParseCommandRequest struct {
	// Command is a command line of the engine. It is tokenised, never run.
	Command string `json:"command"`
}

// EngineCommandPreviewRequest is the body of POST /api/engines/{engine}/command-preview.
type EngineCommandPreviewRequest struct {
	// Config is a partial model config: YAML keys, nested blocks and the
	// options list.
	Config enginesettings.Config `json:"config"`
}

// EnginePlacementRequest is the body of POST /api/engines/{engine}/placement.
// Send either a placement to build an options entry, or an entry to recognise.
type EnginePlacementRequest struct {
	Placement *enginesettings.Placement `json:"placement,omitempty"`
	// Layers is an optional "0-6,18-23" string. It replaces placement.layers.
	Layers string `json:"layers,omitempty"`
	Option string `json:"option,omitempty"`
}

// EnginePlacementResponse is a tensor placement and its options entry.
type EnginePlacementResponse struct {
	Option string `json:"option"`
	// Placement is null when the entry is not one this server builds.
	Placement *enginesettings.Placement `json:"placement"`
	Raw       bool                      `json:"raw"`
}

func engineError(c echo.Context, code int, msg string) error {
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{
		Code: code, Message: msg, Type: "invalid_request_error",
	}})
}

// decodeEngineBody reads a JSON body with a size limit.
func decodeEngineBody(c echo.Context, v any) error {
	body := http.MaxBytesReader(c.Response(), c.Request().Body, maxEngineBody)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return engineError(c, http.StatusRequestEntityTooLarge, "the request body is too large")
		}
		if errors.Is(err, io.EOF) {
			return engineError(c, http.StatusBadRequest, "the request body is empty")
		}
		return engineError(c, http.StatusBadRequest, "the request body is not valid JSON")
	}
	return nil
}

// lookupEngine finds the engine of the request path. It answers 404 for an
// unknown engine and 400 for an engine without the capability, and returns
// nil when it has answered.
func lookupEngine(c echo.Context, reg *enginesettings.Registry, need enginesettings.Capability) (*enginesettings.Engine, error) {
	e, ok := reg.Get(c.Param("engine"))
	if !ok {
		return nil, engineError(c, http.StatusNotFound, "unknown engine "+c.Param("engine"))
	}
	if need != "" && !e.Has(need) {
		return nil, engineError(c, http.StatusBadRequest, "the engine "+e.ID+" does not support "+string(need))
	}
	return e, nil
}

// EngineListEndpoint lists the engines and the backend names each serves.
// @Summary List the engines with runtime settings
// @Description Lists the engines whose runtime settings LocalAI maps to model config keys, with the backend names each serves and its capabilities. With the backend query parameter, lists only the engine that serves that backend name (an empty value is a model with no backend).
// @Tags config
// @Produce json
// @Param backend query string false "A model backend name"
// @Success 200 {object} EngineListResponse "Engines"
// @Router /api/engines [get]
func EngineListEndpoint(reg *enginesettings.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		out := EngineListResponse{Engines: []enginesettings.Info{}}
		if backend, filter := c.QueryParams()["backend"]; filter {
			name := ""
			if len(backend) > 0 {
				name = backend[0]
			}
			if e, ok := reg.ForBackend(name); ok {
				out.Engines = append(out.Engines, e.Info())
			}
			return c.JSON(http.StatusOK, out)
		}
		for _, e := range reg.All() {
			out.Engines = append(out.Engines, e.Info())
		}
		return c.JSON(http.StatusOK, out)
	}
}

// EngineSettingsSchemaEndpoint returns the settings schema of an engine.
// @Summary Get the runtime settings schema of an engine
// @Description Returns the settings of an engine with their model config keys, types, limits, defaults and a doc line, the editor groups, the combined controls, the presets and the capabilities, so a client can render the fields from one source.
// @Tags config
// @Produce json
// @Param engine path string true "Engine id, for example llama-cpp"
// @Success 200 {object} enginesettings.Schema "Settings schema"
// @Failure 404 {object} schema.ErrorResponse "Unknown engine"
// @Router /api/engines/{engine}/settings-schema [get]
func EngineSettingsSchemaEndpoint(reg *enginesettings.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		e, err := lookupEngine(c, reg, "")
		if e == nil {
			return err
		}
		return c.JSON(http.StatusOK, e.Schema())
	}
}

// EngineParseCommandEndpoint turns a pasted command line into model config
// fields.
// @Summary Parse a command line of an engine
// @Description Reads a command line of the engine and returns the model config keys and options it maps to. The command is only tokenised; it is never run. Flags without a first-class key are listed as unmapped and kept as raw passthrough options. Needs the command-line capability.
// @Tags config
// @Accept json
// @Produce json
// @Param engine path string true "Engine id, for example llama-cpp"
// @Param request body EngineParseCommandRequest true "The command line"
// @Success 200 {object} enginesettings.ParseResult "Parsed settings"
// @Failure 400 {object} schema.ErrorResponse "Invalid body or command, or no command-line capability"
// @Failure 404 {object} schema.ErrorResponse "Unknown engine"
// @Failure 413 {object} schema.ErrorResponse "Command too large"
// @Router /api/engines/{engine}/parse-command [post]
func EngineParseCommandEndpoint(reg *enginesettings.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		e, err := lookupEngine(c, reg, enginesettings.CapabilityCommandLine)
		if e == nil {
			return err
		}
		var req EngineParseCommandRequest
		if err := decodeEngineBody(c, &req); err != nil || c.Response().Committed {
			return err
		}
		if len(req.Command) > enginesettings.MaxCommandLength {
			return engineError(c, http.StatusRequestEntityTooLarge, "the command is too long")
		}
		res, err := e.ParseCommand(req.Command)
		if err != nil {
			return engineError(c, http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, res)
	}
}

// EngineCommandPreviewEndpoint writes the command line of a config.
// @Summary Preview the command line of a model config
// @Description Builds the command line of the engine that matches a partial model config. The text is for display; nothing is run. Needs the command-line capability.
// @Tags config
// @Accept json
// @Produce json
// @Param engine path string true "Engine id, for example llama-cpp"
// @Param request body EngineCommandPreviewRequest true "Partial model config"
// @Success 200 {object} enginesettings.BuildResult "Command"
// @Failure 400 {object} schema.ErrorResponse "Invalid body, or no command-line capability"
// @Failure 404 {object} schema.ErrorResponse "Unknown engine"
// @Failure 413 {object} schema.ErrorResponse "Body too large"
// @Router /api/engines/{engine}/command-preview [post]
func EngineCommandPreviewEndpoint(reg *enginesettings.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		e, err := lookupEngine(c, reg, enginesettings.CapabilityCommandLine)
		if e == nil {
			return err
		}
		var req EngineCommandPreviewRequest
		if err := decodeEngineBody(c, &req); err != nil || c.Response().Committed {
			return err
		}
		res, err := e.BuildCommand(req.Config)
		if err != nil {
			return engineError(c, http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, res)
	}
}

// EnginePlacementEndpoint builds or recognises a tensor placement.
// @Summary Build or recognise a tensor placement
// @Description With a placement, returns the options entry that keeps the expert tensors of the chosen layers on the CPU. With an option, returns its structured form, or null when the entry is not one this server builds. Placement is always opt-in: nothing applies it by default. Needs the tensor-placement capability.
// @Tags config
// @Accept json
// @Produce json
// @Param engine path string true "Engine id, for example llama-cpp"
// @Param request body EnginePlacementRequest true "A placement or an option"
// @Success 200 {object} EnginePlacementResponse "Placement"
// @Failure 400 {object} schema.ErrorResponse "Invalid input, or no tensor-placement capability"
// @Failure 404 {object} schema.ErrorResponse "Unknown engine"
// @Failure 413 {object} schema.ErrorResponse "Body too large"
// @Router /api/engines/{engine}/placement [post]
func EnginePlacementEndpoint(reg *enginesettings.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		e, err := lookupEngine(c, reg, enginesettings.CapabilityTensorPlacement)
		if e == nil {
			return err
		}
		var req EnginePlacementRequest
		if err := decodeEngineBody(c, &req); err != nil || c.Response().Committed {
			return err
		}
		pl := e.Placement
		switch {
		case req.Placement != nil && req.Option == "":
			p := *req.Placement
			if req.Layers != "" {
				rs, err := enginesettings.ParseLayerRanges(req.Layers)
				if err != nil {
					return engineError(c, http.StatusBadRequest, err.Error())
				}
				p.Layers = rs
			}
			opt, err := pl.Build(p)
			if err != nil {
				return engineError(c, http.StatusBadRequest, err.Error())
			}
			// Report the form the server would recognise: ranges are merged.
			canon, _ := pl.Recognise(opt)
			return c.JSON(http.StatusOK, EnginePlacementResponse{Option: opt, Placement: &canon})
		case req.Option != "" && req.Placement == nil:
			if len(req.Option) > enginesettings.MaxCommandLength {
				return engineError(c, http.StatusRequestEntityTooLarge, "the option is too long")
			}
			p, ok := pl.Recognise(req.Option)
			if !ok {
				return c.JSON(http.StatusOK, EnginePlacementResponse{Option: req.Option, Raw: true})
			}
			return c.JSON(http.StatusOK, EnginePlacementResponse{Option: req.Option, Placement: &p})
		}
		return engineError(c, http.StatusBadRequest, "send either placement or option")
	}
}
