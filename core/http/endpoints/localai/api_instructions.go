package localai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/swagger"
	"github.com/mudler/xlog"
)

const swaggerDefsPrefix = "#/definitions/"

// instructionDef is a lightweight instruction definition that maps to swagger tags.
type instructionDef struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Intro       string   `json:"-"` // brief context not in swagger
}

var instructionDefs = []instructionDef{
	{
		Name:        "chat-inference",
		Description: "OpenAI-compatible chat completions, text completions, and embeddings",
		Tags:        []string{"inference", "embeddings"},
		Intro:       "Set \"stream\": true for SSE streaming. Supports tool/function calling when the model config has function templates configured.",
	},
	{
		Name:        "audio",
		Description: "Text-to-speech, voice activity detection, transcription, speaker diarization, sound classification, and sound generation",
		Tags:        []string{"audio"},
		Intro:       "Diarization (/v1/audio/diarization) returns speaker-labelled time segments. Backends with native ASR-diarization (vibevoice-cpp) can also emit per-segment text via include_text=true; backends with a dedicated pipeline (sherpa-onnx + pyannote) emit segmentation only. Response formats: json (default), verbose_json (adds speakers summary + text), rttm (NIST format). Sound classification (/v1/audio/classification) returns scored AudioSet sound-event tags (audio tagging via the ced backend); top_k and threshold control the returned set.",
	},
	{
		Name:        "voice-library",
		Description: "Create, preview, list, and delete reusable voice-cloning reference profiles",
		Tags:        []string{"voice-profiles"},
		Intro:       "Profiles persist below the LocalAI data directory as private PCM-WAV references plus exact transcripts. GET and audio preview require the audio_speech feature; create and delete are admin-only. Pass the returned `voice` URI to /v1/audio/speech or /tts. LocalAI resolves the URI at request time and never exposes a filesystem path.",
	},
	{
		Name:        "images",
		Description: "Image generation and inpainting",
		Tags:        []string{"images"},
	},
	{
		Name:        "model-management",
		Description: "Browse the gallery, install, delete, and manage models and backends",
		Tags:        []string{"models", "backends"},
	},
	{
		Name:        "config-management",
		Description: "Discover, read, and modify model configuration fields with VRAM estimation",
		Tags:        []string{"config"},
		Intro:       "Fields with static options include an \"options\" array in metadata. Fields with dynamic values have an \"autocomplete_provider\" for runtime lookup.",
	},
	{
		Name:        "monitoring",
		Description: "System metrics, backend status, API and backend traces, backend process logs, and system information",
		Tags:        []string{"monitoring"},
		Intro:       "Includes real-time backend log streaming via WebSocket at /ws/backend-logs/:modelId.",
	},
	{
		Name:        "mcp",
		Description: "Model Context Protocol — tool-augmented chat with MCP servers",
		Tags:        []string{"mcp"},
		Intro:       "The model's config must define MCP servers. The endpoint handles tool execution automatically.",
	},
	{
		Name:        "agents",
		Description: "Agent task and job management for CI/automation workflows",
		Tags:        []string{"agent-jobs"},
	},
	{
		Name:        "video",
		Description: "Video generation from text prompts with optional image or audio conditioning",
		Tags:        []string{"video"},
		Intro:       "POST /video accepts start_image, end_image, and audio as public URL, base64, or data URI. Backend-specific tuning is passed as string values in params.",
	},
	{
		Name:        "face-recognition",
		Description: "Face verification (1:1), identification (1:N), embedding, and demographic analysis",
		Tags:        []string{"face-recognition"},
		Intro:       "The /v1/face/register, /identify, and /forget endpoints build on a vector store — registrations are in-memory by default and lost on restart. Use /v1/face/embed for a raw embedding; /v1/embeddings is OpenAI-compatible and text-only.",
	},
	{
		Name:        "voice-recognition",
		Description: "Speaker verification (1:1), embedding, and demographic analysis from voice",
		Tags:        []string{"voice-recognition"},
		Intro:       "Voice (speaker) recognition — the audio analog to /v1/face/*. Use /v1/voice/verify for 1:1 speaker comparison, /v1/voice/identify for 1:N match against the registered store, /v1/voice/{register,forget} to manage that store, /v1/voice/embed for a raw speaker-encoder vector, and /v1/voice/analyze for age / gender / emotion inferred from speech. Registrations are in-memory by default and lost on restart. Audio inputs accept URL, base64, or data-URI; /v1/embeddings remains text-only.",
	},
	{
		Name:        "branding",
		Description: "Whitelabel the instance: configure name, tagline, logo, and favicon",
		Tags:        []string{"branding"},
		Intro:       "GET /api/branding is public so the login screen can render the configured logo before authentication. Text fields are saved through POST /api/settings; binary assets (logo, horizontal logo, favicon) use multipart upload at /api/branding/asset/{kind} and are served back from /branding/asset/{kind}.",
	},
	{
		Name:        "usage-and-billing",
		Description: "Per-user token usage and request counts, with optional cost tracking",
		Tags:        []string{"usage"},
		Intro:       "GET /api/usage returns the current user's token usage in time-bucketed form (day/week/month/all). In single-user no-auth mode the records are attributed to a synthetic local user with stable UUID, so this endpoint and the dashboard work without --auth. /api/usage/all is the cluster-wide view and requires admin (the local user is admin in single-user mode). UsageRecord fields include RequestedModel/ServedModel and PreFilter/PostFilterPromptTokens for routing- and PII-aware accounting.",
	},
	{
		Name:        "pii-filtering",
		Description: "Inspect the NER-based PII filter applied to chat requests",
		Tags:        []string{"pii"},
		Intro:       "PII redaction is NER-based and request-side. A consuming model opts in with `pii: { enabled: true, detectors: [<model>] }` where each detector is a token-classification (token_classify) model. The detection policy lives on the detector model itself in a `pii_detection:` block: `{ min_score, default_action (mask|block|allow), entity_actions: { GROUP: action } }`. Multiple detectors union their hits; overlapping spans resolve to the strongest action (block > mask > allow). PII defaults OFF for non-proxy backends and ON for proxy-* (cloud passthroughs). Besides the inline path, two synchronous service endpoints expose the same engine without an inference request: POST /api/pii/analyze returns the detected entity spans (entity_type, source ner|pattern, start/end, score, action) without mutating the text, and POST /api/pii/redact applies the policy — returning redacted_text, or 400 (type pii_blocked) with the offending entities when a block action fires. Both take `{ text, detectors:[<model>...] }` (or `model` to inherit a consuming model's detectors), require the pii_filter feature (any authenticated user), and record audit events with an `origin` of pii_analyze / pii_redact. GET /api/pii/events returns recent redaction events filtered by correlation_id / user_id / pattern_id / origin (middleware|proxy|pii_analyze|pii_redact); events carry `<source>:<GROUP>` ids — e.g. `ner:EMAIL` for the neural detector, `pattern:ANTHROPIC_KEY` for the regex pattern tier — and an 8-char hash prefix, never the matched value (admin or local-user only). The legacy regex pattern tier and its endpoints (/api/pii/patterns, /test, /decide) were removed.",
	},
	{
		Name:        "middleware-admin",
		Description: "Inspect and configure the routing-module middleware (PII filter and routing)",
		Tags:        []string{"middleware", "pii", "router"},
		Intro:       "GET /api/middleware/status is the single round-trip the /app/middleware admin page reads to render the current state: every model's resolved PII enabled state and the NER detector models it references, recent event count, and the active routing models with their classifier configurations. Admin-only (the synthetic local user is admin in no-auth mode). PII detection policy is edited on each detector model's `pii_detection:` block via the model-config tools/UI — there is no global pattern set to mutate. GET /api/router/decisions returns the routing decision log filtered by correlation_id / user_id / router_model. The same surface is exposed as MCP tools (`get_middleware_status`, `get_pii_events`, `get_router_decisions`) for agent-driven inspection.",
	},
	{
		Name:        "intelligent-routing",
		Description: "Per-model `router:` configuration that classifies requests and rewrites the served model",
		Tags:        []string{"router"},
		Intro:       "Add a `router:` block to a ModelConfig to turn it into a routing model. The block declares a classifier (`score` — a small model ranks each policy label, Arch-Router-style; `colbert` — a reranker scores policy descriptions against the prompt; `knn` — similarity-weighted vote over a curated corpus of labelled example prompts), `policies` (the label vocabulary), `candidates` (downstream model + labels it serves; first candidate whose labels cover the active set wins, so order small → large), and a `fallback`. The knn classifier needs a `knn: { embedding_model }` block instead of a classifier_model, and reads a persisted corpus seeded via POST /api/router/{name}/corpus with `{entries: [{text, labels}]}` (admin-only; texts are embedded server-side, persisted under the state dir, and NEVER returned by any endpoint — GET /api/router/{name}/corpus/stats reports label counts only, DELETE /api/router/{name}/corpus wipes it). knn routes to the fallback whenever the prompt is less similar than knn.similarity_threshold to every corpus entry — out-of-corpus prompts are treated as undecidable rather than guessed. When a client addresses the routing model, the RouteModel middleware invokes the classifier, picks a candidate, and rewrites input.Model — the standard model-resolution path then runs ACL, disabled-state, and per-model PII against the chosen target. Depth-1 invariant: candidates must NOT themselves carry a `router:` block; runtime check returns 500 on violation. Decisions are logged to GET /api/router/decisions and surfaced in the /app/middleware Routing tab. POST /api/router/decide is the programmatic decision-oracle: external routers (e.g. an organisation-wide router service) send `{router, input}` and receive the classifier's label set + candidate model WITHOUT LocalAI rewriting, forwarding, or recording the call. Shares the classifier cache with the in-band path so warm-up costs are paid once.",
	},
}

// swaggerState holds parsed swagger spec data, initialised once.
type swaggerState struct {
	once  sync.Once
	spec  map[string]any // full parsed swagger JSON
	ready bool
}

var swState swaggerState

func (s *swaggerState) init() {
	s.once.Do(func() {
		var spec map[string]any
		if err := json.Unmarshal(swagger.SwaggerJSON, &spec); err != nil {
			xlog.Error("failed to parse embedded swagger spec", "err", err)
			return
		}
		s.spec = spec
		s.ready = true
	})
}

// filterSwaggerByTags returns a swagger fragment containing only paths whose
// operations carry at least one of the given tags, plus the definitions they
// reference.
func filterSwaggerByTags(spec map[string]any, tags []string) map[string]any {
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}

	paths, _ := spec["paths"].(map[string]any)
	allDefs, _ := spec["definitions"].(map[string]any)

	filteredPaths := make(map[string]any)
	for path, methods := range paths {
		methodMap, ok := methods.(map[string]any)
		if !ok {
			continue
		}
		filteredMethods := make(map[string]any)
		for method, opRaw := range methodMap {
			op, ok := opRaw.(map[string]any)
			if !ok {
				continue
			}
			opTags, _ := op["tags"].([]any)
			for _, t := range opTags {
				if ts, ok := t.(string); ok && tagSet[ts] {
					filteredMethods[method] = op
					break
				}
			}
		}
		if len(filteredMethods) > 0 {
			filteredPaths[path] = filteredMethods
		}
	}

	// Collect all $ref definitions used by the filtered paths.
	neededDefs := make(map[string]bool)
	collectRefs(filteredPaths, neededDefs)

	// Resolve nested refs from definitions themselves.
	changed := true
	for changed {
		changed = false
		for name := range neededDefs {
			if def, ok := allDefs[name]; ok {
				before := len(neededDefs)
				collectRefs(def, neededDefs)
				if len(neededDefs) > before {
					changed = true
				}
			}
		}
	}

	filteredDefs := make(map[string]any)
	for name := range neededDefs {
		if def, ok := allDefs[name]; ok {
			filteredDefs[name] = def
		}
	}

	result := map[string]any{
		"paths": filteredPaths,
	}
	if len(filteredDefs) > 0 {
		result["definitions"] = filteredDefs
	}
	return result
}

// collectRefs walks a JSON structure and collects all $ref definition names.
func collectRefs(v any, refs map[string]bool) {
	switch val := v.(type) {
	case map[string]any:
		if ref, ok := val["$ref"].(string); ok {
			if strings.HasPrefix(ref, swaggerDefsPrefix) {
				refs[ref[len(swaggerDefsPrefix):]] = true
			}
		}
		for _, child := range val {
			collectRefs(child, refs)
		}
	case []any:
		for _, child := range val {
			collectRefs(child, refs)
		}
	}
}

// swaggerToMarkdown renders a filtered swagger fragment into concise markdown.
func swaggerToMarkdown(skillName, intro string, fragment map[string]any) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(skillName)
	b.WriteString("\n")
	if intro != "" {
		b.WriteString("\n")
		b.WriteString(intro)
		b.WriteString("\n")
	}

	paths, _ := fragment["paths"].(map[string]any)
	defs, _ := fragment["definitions"].(map[string]any)

	// Sort paths for stable output.
	sortedPaths := make([]string, 0, len(paths))
	for p := range paths {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)

	for _, path := range sortedPaths {
		methods, ok := paths[path].(map[string]any)
		if !ok {
			continue
		}
		sortedMethods := sortMethods(methods)
		for _, method := range sortedMethods {
			op, ok := methods[method].(map[string]any)
			if !ok {
				continue
			}
			summary, _ := op["summary"].(string)
			b.WriteString(fmt.Sprintf("\n## %s %s\n", strings.ToUpper(method), path))
			if summary != "" {
				b.WriteString(summary)
				b.WriteString("\n")
			}

			// Parameters
			params, _ := op["parameters"].([]any)
			bodyParams, nonBodyParams := splitParams(params)

			if len(nonBodyParams) > 0 {
				b.WriteString("\n**Parameters:**\n")
				b.WriteString("| Name | In | Type | Required | Description |\n")
				b.WriteString("|------|----|------|----------|-------------|\n")
				for _, p := range nonBodyParams {
					pm, ok := p.(map[string]any)
					if !ok {
						continue
					}
					name, _ := pm["name"].(string)
					in, _ := pm["in"].(string)
					typ, _ := pm["type"].(string)
					req, _ := pm["required"].(bool)
					desc, _ := pm["description"].(string)
					b.WriteString(fmt.Sprintf("| %s | %s | %s | %v | %s |\n", name, in, typ, req, desc))
				}
			}

			if len(bodyParams) > 0 {
				for _, p := range bodyParams {
					pm, ok := p.(map[string]any)
					if !ok {
						continue
					}
					schema, _ := pm["schema"].(map[string]any)
					refName := resolveRefName(schema)
					if refName != "" {
						b.WriteString(fmt.Sprintf("\n**Request body** (`%s`):\n", refName))
						renderSchemaFields(&b, refName, defs)
					}
				}
			}

			// Responses
			responses, _ := op["responses"].(map[string]any)
			if len(responses) > 0 {
				sortedCodes := make([]string, 0, len(responses))
				for code := range responses {
					sortedCodes = append(sortedCodes, code)
				}
				sort.Strings(sortedCodes)
				for _, code := range sortedCodes {
					resp, ok := responses[code].(map[string]any)
					if !ok {
						continue
					}
					desc, _ := resp["description"].(string)
					respSchema, _ := resp["schema"].(map[string]any)
					refName := resolveRefName(respSchema)
					if refName != "" {
						b.WriteString(fmt.Sprintf("\n**Response %s** (`%s`): %s\n", code, refName, desc))
						renderSchemaFields(&b, refName, defs)
					} else if desc != "" {
						b.WriteString(fmt.Sprintf("\n**Response %s**: %s\n", code, desc))
					}
				}
			}
		}
	}

	return b.String()
}

// sortMethods returns HTTP methods in a conventional order.
func sortMethods(methods map[string]any) []string {
	order := map[string]int{"get": 0, "post": 1, "put": 2, "patch": 3, "delete": 4}
	keys := make([]string, 0, len(methods))
	for k := range methods {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		oi, oki := order[keys[i]]
		oj, okj := order[keys[j]]
		if !oki {
			oi = 99
		}
		if !okj {
			oj = 99
		}
		return oi < oj
	})
	return keys
}

// splitParams separates body parameters from non-body parameters.
func splitParams(params []any) (body, nonBody []any) {
	for _, p := range params {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if in, _ := pm["in"].(string); in == "body" {
			body = append(body, p)
		} else {
			nonBody = append(nonBody, p)
		}
	}
	return
}

// resolveRefName extracts the definition name from a $ref or returns "".
func resolveRefName(schema map[string]any) string {
	if schema == nil {
		return ""
	}
	if ref, ok := schema["$ref"].(string); ok {
		if strings.HasPrefix(ref, swaggerDefsPrefix) {
			return ref[len(swaggerDefsPrefix):]
		}
	}
	return ""
}

// renderSchemaFields writes a markdown field table for a definition.
func renderSchemaFields(b *strings.Builder, defName string, defs map[string]any) {
	if defs == nil {
		return
	}
	def, ok := defs[defName].(map[string]any)
	if !ok {
		return
	}
	props, ok := def["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return
	}

	// Sort fields
	fields := make([]string, 0, len(props))
	for f := range props {
		fields = append(fields, f)
	}
	sort.Strings(fields)

	b.WriteString("| Field | Type | Description |\n")
	b.WriteString("|-------|------|-------------|\n")
	for _, field := range fields {
		prop, ok := props[field].(map[string]any)
		if !ok {
			continue
		}
		typ := schemaTypeString(prop)
		desc, _ := prop["description"].(string)
		b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", field, typ, desc))
	}
}

// schemaTypeString returns a human-readable type string for a schema property.
func schemaTypeString(prop map[string]any) string {
	if ref := resolveRefName(prop); ref != "" {
		return ref
	}
	typ, _ := prop["type"].(string)
	if typ == "array" {
		items, _ := prop["items"].(map[string]any)
		if items != nil {
			if ref := resolveRefName(items); ref != "" {
				return "[]" + ref
			}
			it, _ := items["type"].(string)
			if it != "" {
				return "[]" + it
			}
		}
		return "[]any"
	}
	if typ != "" {
		return typ
	}
	return "object"
}

// APIInstructionResponse is the JSON response for a single instruction (?format=json).
type APIInstructionResponse struct {
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Tags            []string       `json:"tags"`
	SwaggerFragment map[string]any `json:"swagger_fragment,omitempty"`
}

// ListAPIInstructionsEndpoint returns all instructions (compact list without guides).
// @Summary List available API instruction areas
// @Description Returns a compact list of instruction areas with descriptions and URLs for detailed guides
// @Tags instructions
// @Produce json
// @Success 200 {object} map[string]any "instructions list with hint"
// @Router /api/instructions [get]
func ListAPIInstructionsEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		type compactInstruction struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
			URL         string   `json:"url"`
		}
		instructions := make([]compactInstruction, len(instructionDefs))
		for i, s := range instructionDefs {
			instructions[i] = compactInstruction{
				Name:        s.Name,
				Description: s.Description,
				Tags:        s.Tags,
				URL:         "/api/instructions/" + s.Name,
			}
		}
		return c.JSON(http.StatusOK, map[string]any{
			"instructions": instructions,
			"hint":         "Fetch GET {url} for a markdown API guide. Add ?format=json for a raw OpenAPI fragment.",
		})
	}
}

// GetAPIInstructionEndpoint returns a single instruction by name.
// @Summary Get an instruction's API guide or OpenAPI fragment
// @Description Returns a markdown guide (default) or filtered OpenAPI fragment (format=json) for a named instruction
// @Tags instructions
// @Produce json
// @Produce text/markdown
// @Param name path string true "Instruction name (e.g. chat-inference, config-management)"
// @Param format query string false "Response format: json for OpenAPI fragment, omit for markdown"
// @Success 200 {object} APIInstructionResponse "instruction documentation"
// @Failure 404 {object} map[string]string "instruction not found"
// @Router /api/instructions/{name} [get]
func GetAPIInstructionEndpoint() echo.HandlerFunc {
	byName := make(map[string]*instructionDef, len(instructionDefs))
	for i := range instructionDefs {
		byName[instructionDefs[i].Name] = &instructionDefs[i]
	}

	return func(c echo.Context) error {
		name := c.Param("name")
		inst, ok := byName[name]
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]any{"error": "instruction not found: " + name})
		}

		swState.init()
		if !swState.ready {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": "swagger spec not available"})
		}

		fragment := filterSwaggerByTags(swState.spec, inst.Tags)

		format := c.QueryParam("format")
		if format == "json" {
			return c.JSON(http.StatusOK, APIInstructionResponse{
				Name:            inst.Name,
				Description:     inst.Description,
				Tags:            inst.Tags,
				SwaggerFragment: fragment,
			})
		}

		guide := swaggerToMarkdown(inst.Name, inst.Intro, fragment)
		return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", []byte(guide))
	}
}
