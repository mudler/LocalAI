package localai

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
)

// ---------------------------------------------------------------------------
// Helpers — ported from kev/api.py (render, r2, choice_confidence,
// score_confidence, softmax) and mirrored in vllm.cpp api_server.cpp.
// ---------------------------------------------------------------------------

// renderState flattens a JSON value into text, mirroring kev's render().
// Field names are kept as labels; arrays become "- item" bullets; objects
// become "key: value" lines.
func renderState(v interface{}, indent int) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case float64:
		b, _ := json.Marshal(val)
		return string(b)
	case []interface{}:
		pad := strings.Repeat("  ", indent)
		var parts []string
		for _, item := range val {
			rendered := renderState(item, indent+1)
			rendered = strings.TrimLeft(rendered, " \t\n")
			parts = append(parts, pad+"- "+rendered)
		}
		return strings.Join(parts, "\n")
	case map[string]interface{}:
		pad := strings.Repeat("  ", indent)
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for i, k := range keys {
			if i > 0 {
				parts = append(parts, "\n")
			}
			switch vv := val[k].(type) {
			case map[string]interface{}, []interface{}:
				parts = append(parts, pad+k+":\n"+renderState(vv, indent+1))
			default:
				parts = append(parts, pad+k+": "+renderState(vv, indent))
			}
		}
		return strings.Join(parts, "")
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// r2 rounds to 2 decimal places.
func r2(x float64) float64 {
	return math.Round(x*100) / 100
}

// choiceConfidence is the normalized margin (kev/api.py:choice_confidence).
func choiceConfidence(p []float64) float64 {
	k := len(p)
	if k <= 1 {
		return 1.0
	}
	mx := p[0]
	for _, v := range p[1:] {
		if v > mx {
			mx = v
		}
	}
	return (mx - 1.0/float64(k)) / (1.0 - 1.0/float64(k))
}

// scoreConfidence is 1 - E|level - mode| / (L - 1)
// (kev/api.py:score_confidence).
func scoreConfidence(p []float64) float64 {
	l := len(p)
	if l <= 1 {
		return 1.0
	}
	mode := 0
	maxP := p[0]
	for i := 1; i < l; i++ {
		if p[i] > maxP {
			maxP = p[i]
			mode = i
		}
	}
	s := 0.0
	for i := 0; i < l; i++ {
		s += p[i] * math.Abs(float64(i)-float64(mode))
	}
	return 1.0 - s/float64(l-1)
}

// softmax is a numerically stable softmax.
func softmax(scores []float64) []float64 {
	if len(scores) == 0 {
		return nil
	}
	mx := scores[0]
	for _, s := range scores[1:] {
		if s > mx {
			mx = s
		}
	}
	exps := make([]float64, len(scores))
	sum := 0.0
	for i, s := range scores {
		exps[i] = math.Exp(s - mx)
		sum += exps[i]
	}
	if sum <= 0 {
		inv := 1.0 / float64(len(scores))
		for i := range exps {
			exps[i] = inv
		}
		return exps
	}
	for i := range exps {
		exps[i] /= sum
	}
	return exps
}

// optionText mirrors kev/api.py:option_text. "name" if desc is null/empty,
// else "name: rendered_desc".
func optionText(name string, desc json.RawMessage) string {
	if len(desc) == 0 || string(desc) == "null" {
		return name
	}
	var v interface{}
	if err := json.Unmarshal(desc, &v); err != nil {
		return name
	}
	rendered := renderState(v, 0)
	if rendered == "" {
		return name
	}
	return name + ": " + rendered
}

// getQuestionInstructions checks instructions first, then instr (alias).
// The value is rendered to text, matching vllm.cpp GetInstructions.
func getQuestionInstructions(q schema.SystemOneQuestion) string {
	if len(q.Instructions) > 0 {
		var v interface{}
		if err := json.Unmarshal(q.Instructions, &v); err == nil {
			return renderState(v, 0)
		}
	}
	return q.Instr
}

// ---------------------------------------------------------------------------
// Parsed question (internal).
// ---------------------------------------------------------------------------

type parsedQuestion struct {
	id     string
	qtype  string // "noul", "choice", "score"
	keys   []string
	labels []string
}

type parsedSystemOne struct {
	text      string
	model     string
	threshold float32
	questions []parsedQuestion
	allLabels []string
}

func parseSystemOneRequest(req *schema.SystemOneRequest) (*parsedSystemOne, error) {
	p := &parsedSystemOne{
		model:     req.Model,
		threshold: 0.5,
	}
	if req.Threshold != nil {
		p.threshold = *req.Threshold
	}

	var stateVal interface{}
	if err := json.Unmarshal(req.State, &stateVal); err != nil {
		return nil, fmt.Errorf("state is not valid JSON: %w", err)
	}
	p.text = renderState(stateVal, 0)

	if len(req.Questions) == 0 {
		return nil, fmt.Errorf("questions is required and must contain at least one question")
	}

	qids := make([]string, 0, len(req.Questions))
	for k := range req.Questions {
		qids = append(qids, k)
	}
	sort.Strings(qids)

	for _, qid := range qids {
		q := req.Questions[qid]
		pq := parsedQuestion{id: qid, qtype: q.Type}
		switch q.Type {
		case "noul":
			instr := getQuestionInstructions(q)
			pq.labels = []string{instr}
			pq.keys = []string{"no", "yes"}
		case "choice":
			var criteria map[string]json.RawMessage
			if err := json.Unmarshal(q.Criteria, &criteria); err != nil || len(criteria) == 0 {
				return nil, fmt.Errorf("question %q (choice) requires a non-empty criteria object", qid)
			}
			ckeys := make([]string, 0, len(criteria))
			for k := range criteria {
				ckeys = append(ckeys, k)
			}
			sort.Strings(ckeys)
			for _, ck := range ckeys {
				pq.keys = append(pq.keys, ck)
				pq.labels = append(pq.labels, optionText(ck, criteria[ck]))
			}
		case "score":
			var criteria []json.RawMessage
			if err := json.Unmarshal(q.Criteria, &criteria); err != nil || len(criteria) < 2 {
				return nil, fmt.Errorf("question %q (score) requires a criteria array with >= 2 levels", qid)
			}
			for _, level := range criteria {
				var lv interface{}
				_ = json.Unmarshal(level, &lv)
				rendered := renderState(lv, 0)
				pq.keys = append(pq.keys, rendered)
				pq.labels = append(pq.labels, rendered)
			}
		default:
			return nil, fmt.Errorf("question %q has unknown type: %s", qid, q.Type)
		}
		p.questions = append(p.questions, pq)
		p.allLabels = append(p.allLabels, pq.labels...)
	}
	return p, nil
}

// buildSystemOneAnswer produces one kev answer from NER entities.
func buildSystemOneAnswer(q *parsedQuestion, entities []backend.TokenEntity) schema.SystemOneAnswer {
	scores := make([]float64, len(q.labels))
	for i, label := range q.labels {
		var maxConf float32
		for _, e := range entities {
			if e.Group == label && e.Score > maxConf {
				maxConf = e.Score
			}
		}
		scores[i] = float64(maxConf)
	}

	switch q.qtype {
	case "noul":
		probs := []float64{1.0 - scores[0], scores[0]}
		var ents []schema.SystemOneEntity
		for _, e := range entities {
			if e.Group == q.labels[0] {
				ents = append(ents, schema.SystemOneEntity{
					Text:       e.Text,
					Start:      e.Start,
					End:        e.End,
					Confidence: e.Score,
				})
			}
		}
		noul := r2(probs[1])
		return schema.SystemOneAnswer{
			Type:     "noul",
			Noul:     &noul,
			Entities: ents,
		}

	case "choice":
		probs := softmax(scores)
		argmax := 0
		for i := 1; i < len(probs); i++ {
			if probs[i] > probs[argmax] {
				argmax = i
			}
		}
		dist := make(map[string]float64, len(q.keys))
		for i, k := range q.keys {
			dist[k] = r2(probs[i])
		}
		choice := q.keys[argmax]
		conf := r2(choiceConfidence(probs))
		return schema.SystemOneAnswer{
			Type:          "choice",
			Choice:        &choice,
			Confidence:    &conf,
			Probabilities: dist,
		}

	default: // score
		probs := softmax(scores)
		var score float64
		for i, pr := range probs {
			score += float64(i) * pr
		}
		legend := make(map[string]string, len(q.keys))
		dist := make(map[string]float64, len(q.keys))
		for i, k := range q.keys {
			legend[strconv.Itoa(i)] = k
			dist[strconv.Itoa(i)] = r2(probs[i])
		}
		sc := r2(score)
		conf := r2(scoreConfidence(probs))
		return schema.SystemOneAnswer{
			Type:          "score",
			Score:         &sc,
			Legend:        legend,
			Probabilities: dist,
			Confidence:    &conf,
		}
	}
}

// ---------------------------------------------------------------------------
// Model resolution.
// ---------------------------------------------------------------------------

func resolveClassifier(app *application.Application, modelName string, threshold float32) (backend.TokenClassifier, error) {
	cl := app.ModelConfigLoader()
	if cl == nil {
		return nil, fmt.Errorf("model config loader unavailable")
	}
	cfg, ok := cl.GetModelConfig(modelName)
	if !ok {
		return nil, fmt.Errorf("model %q not found", modelName)
	}
	opts := backend.TokenClassifyOptions{
		Threshold: threshold,
	}
	return backend.NewTokenClassifier(app.ModelLoader(), cfg, app.ApplicationConfig(), opts), nil
}

func systemOneError(c echo.Context, status int, msg string) error {
	return c.JSON(status, map[string]any{
		"error": map[string]string{
			"message": msg,
			"type":    "invalid_request",
		},
	})
}

// systemOneModelAllowed keeps chat and embedding models out of the decision
// API with an actionable error instead of a backend failure. A config that
// declares no usecases predates the flag and stays allowed, and a
// token_classify model is allowed because the NER path serves it.
func systemOneModelAllowed(cfg config.ModelConfig) error {
	return systemone.ModelAllowed(cfg)
}

// checkSystemOneModel applies systemOneModelAllowed to a model looked up by
// name. An unknown model passes here so the existing not-found handling
// downstream keeps its status code.
func checkSystemOneModel(app *application.Application, modelName string) error {
	cl := app.ModelConfigLoader()
	if cl == nil {
		return nil
	}
	cfg, ok := cl.GetModelConfig(modelName)
	if !ok {
		return nil
	}
	return systemOneModelAllowed(cfg)
}

// systemOneUsesDecisionPipeline reports whether /v1/systemone forwards the
// request to the backend's Score RPC (the decision pipeline) for this model.
// A model that declares token_classify without systemone is a zero-shot NER
// model: the backend's decision entry point refuses those architectures, so it
// goes to the NER path instead. A config that declares nothing keeps the
// decision pipeline, which is what setups that predate the decisions usecase
// relied on.
func systemOneUsesDecisionPipeline(cfg config.ModelConfig) bool {
	return systemone.UsesDecisionPipeline(cfg)
}

// systemOneNERAllowed guards /permute and /separate, which always run the NER
// path. A decision model cannot serve them: the backend's NER entry point
// refuses its architecture, and the caller would see a backend error.
func systemOneNERAllowed(cfg config.ModelConfig) error {
	return systemone.NERAllowed(cfg)
}

// checkSystemOneNERModel applies systemOneNERAllowed to a model looked up by
// name; an unknown model passes so the not-found handling keeps its status.
func checkSystemOneNERModel(app *application.Application, modelName string) error {
	cl := app.ModelConfigLoader()
	if cl == nil {
		return nil
	}
	cfg, ok := cl.GetModelConfig(modelName)
	if !ok {
		return nil
	}
	return systemOneNERAllowed(cfg)
}

// systemOneMaxBody and systemOneMaxQuestions bound one request. They keep a
// single call from pinning a decision model on an unbounded prompt, and match
// the limits Ollama documents for the same wire contract, so a client written
// for one server behaves the same on the other. The engine enforces any
// per-model option cap (letter-answer models refuse more than 26 options).
const (
	systemOneMaxBody      = systemone.MaxBodyBytes
	systemOneMaxQuestions = systemone.MaxQuestions
)

// systemOneBind binds the JSON body with a size cap. Bind reads the whole body
// first, so the cap has to be on the reader.
func systemOneBind(c echo.Context, v any) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, systemOneMaxBody)
	return c.Bind(v)
}

// systemOneBindStatus maps a bind failure to its status: 413 when the body
// exceeded the cap, 400 for anything else.
func systemOneBindStatus(err error) int {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func systemOneBindMessage(err error) string {
	if systemOneBindStatus(err) == http.StatusRequestEntityTooLarge {
		return fmt.Sprintf("request body exceeds %d KiB", systemOneMaxBody>>10)
	}
	return "invalid request body"
}

// validateSystemOneRequest checks the structure every path needs, before the
// request is forwarded to a decision model or run through the NER path. The
// forwarded path never sees parseSystemOneRequest, so without this a malformed
// question would surface as a backend error instead of a 400.
func validateSystemOneRequest(req *schema.SystemOneRequest) error {
	return systemone.ValidateRequest(req)
}

// backendSupportsScore reports whether the named backend implements the
// Score gRPC RPC. vllm-cpp does (kev/laya decision pipeline and cua-s1-forms
// scoring via the unified vllm_decide C ABI); other backends fall through to
// the NER-based path.
func backendSupportsScore(backendName string) bool {
	return systemone.BackendSupportsScore(backendName)
}

// ---------------------------------------------------------------------------
// Endpoints.
// ---------------------------------------------------------------------------

// SystemOneEndpoint handles POST /v1/systemone.
// For vllm-cpp models (kev/laya), forwards the raw request to the backend's
// Score gRPC RPC with question_type set to "systemone" and returns the
// response JSON as-is. For other backends, runs one NER pass over the
// rendered state with all question labels, then builds a kev-compatible
// answer for each question.
// @Summary Answer structured-extraction questions over state text.
// @Description Runs zero-shot NER over the supplied state and answers each question. Question types: noul (binary entity presence), choice (pick one option), score (pick one level).
// @Tags systemone
// @Param request body schema.SystemOneRequest true "state + questions"
// @Success 200 {object} schema.SystemOneResponse
// @Router /v1/systemone [post]
func SystemOneEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.SystemOneRequest
		if err := systemOneBind(c, &req); err != nil {
			return systemOneError(c, systemOneBindStatus(err), systemOneBindMessage(err))
		}
		if req.Model == "" {
			return systemOneError(c, http.StatusBadRequest, "model is required")
		}
		if err := checkSystemOneModel(app, req.Model); err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		if err := validateSystemOneRequest(&req); err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		// vllm-cpp models (kev/laya) implement the decision pipeline natively
		// via the vllm_decide C ABI. Forward the raw request JSON through the
		// Score RPC and return the backend's response as-is.
		cl := app.ModelConfigLoader()
		if cl != nil {
			if cfg, ok := cl.GetModelConfig(req.Model); ok && systemOneUsesDecisionPipeline(cfg) {
				reqJSON, err := json.Marshal(req)
				if err != nil {
					return systemOneError(c, http.StatusInternalServerError, "failed to marshal request: "+err.Error())
				}
				fn, err := backend.ModelSystemOne(string(reqJSON), app.ModelLoader(), cfg, app.ApplicationConfig())
				if err != nil {
					return systemOneError(c, http.StatusInternalServerError, err.Error())
				}
				respJSON, err := fn(c.Request().Context())
				if err != nil {
					return systemOneError(c, http.StatusInternalServerError, err.Error())
				}
				return c.JSON(http.StatusOK, json.RawMessage(respJSON))
			}
		}
		// NER-based path (GLiNER2.5 zero-shot NER).
		parsed, err := parseSystemOneRequest(&req)
		if err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		classifier, err := resolveClassifier(app, req.Model, parsed.threshold)
		if err != nil {
			return systemOneError(c, http.StatusNotFound, err.Error())
		}
		start := time.Now()
		entities, err := classifier.TokenClassifyWithLabels(c.Request().Context(), parsed.text, parsed.allLabels)
		if err != nil {
			return systemOneError(c, http.StatusInternalServerError, err.Error())
		}
		latencyMs := float64(time.Since(start).Microseconds()) / 1000.0
		answers := make(map[string]schema.SystemOneAnswer, len(parsed.questions))
		for i := range parsed.questions {
			answers[parsed.questions[i].id] = buildSystemOneAnswer(&parsed.questions[i], entities)
		}
		return c.JSON(http.StatusOK, schema.SystemOneResponse{
			Model:     req.Model,
			Answers:   answers,
			Usage:     schema.SystemOneUsage{InputTokens: 0, OutputTokens: 0},
			LatencyMs: r2(latencyMs),
		})
	}
}

// SystemOnePermuteEndpoint handles POST /v1/systemone/permute.
// Re-runs one choice question under n_perm option orders with a seeded RNG.
// @Summary Re-run a choice question under multiple option orders.
// @Description Re-runs one choice question under n_perm option orders. Reports per-order probabilities, argmax stability, and spread.
// @Tags systemone
// @Param request body schema.SystemOnePermuteRequest true "request + question + n_perm + seed"
// @Success 200 {object} schema.SystemOnePermuteResponse
// @Router /v1/systemone/permute [post]
func SystemOnePermuteEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.SystemOnePermuteRequest
		if err := systemOneBind(c, &req); err != nil {
			return systemOneError(c, systemOneBindStatus(err), systemOneBindMessage(err))
		}
		if req.Request.Model == "" {
			return systemOneError(c, http.StatusBadRequest, "model is required")
		}
		if err := checkSystemOneModel(app, req.Request.Model); err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		if err := checkSystemOneNERModel(app, req.Request.Model); err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		if err := validateSystemOneRequest(&req.Request); err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		if req.Question == "" {
			return systemOneError(c, http.StatusBadRequest, "question is required")
		}
		parsed, err := parseSystemOneRequest(&req.Request)
		if err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		var target *parsedQuestion
		for i := range parsed.questions {
			if parsed.questions[i].id == req.Question {
				target = &parsed.questions[i]
				break
			}
		}
		if target == nil {
			return systemOneError(c, http.StatusBadRequest, fmt.Sprintf("question %q not found", req.Question))
		}
		if target.qtype != "choice" {
			return systemOneError(c, http.StatusBadRequest, "question must be a choice question")
		}
		classifier, err := resolveClassifier(app, req.Request.Model, parsed.threshold)
		if err != nil {
			return systemOneError(c, http.StatusNotFound, err.Error())
		}
		nPerm := req.NPerm
		if nPerm <= 0 {
			nPerm = 6
		}
		rng := rand.New(rand.NewSource(req.Seed)) // #nosec G404 -- seeded RNG for reproducible permutations, not crypto
		runs := make([]schema.SystemOnePermuteRun, 0, nPerm)
		minProb := make([]float64, len(target.keys))
		maxProb := make([]float64, len(target.keys))
		for i := range minProb {
			minProb[i] = 1.0
			maxProb[i] = 0.0
		}
		firstChoice := ""
		argmaxStable := true

		for i := 0; i < nPerm; i++ {
			idx := make([]int, len(target.keys))
			for j := range idx {
				idx[j] = j
			}
			if i > 0 {
				rng.Shuffle(len(idx), func(a, b int) { idx[a], idx[b] = idx[b], idx[a] })
			}
			orderKeys := make([]string, len(idx))
			orderLabels := make([]string, len(idx))
			for j, k := range idx {
				orderKeys[j] = target.keys[k]
				orderLabels[j] = target.labels[k]
			}
			start := time.Now()
			entities, err := classifier.TokenClassifyWithLabels(c.Request().Context(), parsed.text, orderLabels)
			if err != nil {
				return systemOneError(c, http.StatusInternalServerError, err.Error())
			}
			latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

			scores := make([]float64, len(orderLabels))
			for j, label := range orderLabels {
				var maxConf float32
				for _, e := range entities {
					if e.Group == label && e.Score > maxConf {
						maxConf = e.Score
					}
				}
				scores[j] = float64(maxConf)
			}
			probs := softmax(scores)
			argmax := 0
			for j := 1; j < len(probs); j++ {
				if probs[j] > probs[argmax] {
					argmax = j
				}
			}
			probDist := make(map[string]float64, len(orderKeys))
			for j, key := range orderKeys {
				probDist[key] = r2(probs[j])
				for k, tk := range target.keys {
					if key == tk {
						if probs[j] < minProb[k] {
							minProb[k] = probs[j]
						}
						if probs[j] > maxProb[k] {
							maxProb[k] = probs[j]
						}
						break
					}
				}
			}
			choice := orderKeys[argmax]
			if i == 0 {
				firstChoice = choice
			} else if choice != firstChoice {
				argmaxStable = false
			}
			runs = append(runs, schema.SystemOnePermuteRun{
				Order:         orderKeys,
				Probabilities: probDist,
				Choice:        choice,
				LatencyMs:     r2(latencyMs),
			})
		}

		spread := make(map[string]float64, len(target.keys))
		for k, key := range target.keys {
			spread[key] = r2(maxProb[k] - minProb[k])
		}
		return c.JSON(http.StatusOK, schema.SystemOnePermuteResponse{
			Runs:         runs,
			ArgmaxStable: argmaxStable,
			Spread:       spread,
		})
	}
}

// SystemOneSeparateEndpoint handles POST /v1/systemone/separate.
// Answers each question in its own NER call (N passes). Response shape
// matches /v1/systemone.
// @Summary Answer each question in a separate NER pass.
// @Description Runs N independent NER passes, one per question, against the same state. Response shape matches /v1/systemone.
// @Tags systemone
// @Param request body schema.SystemOneRequest true "state + questions"
// @Success 200 {object} schema.SystemOneResponse
// @Router /v1/systemone/separate [post]
func SystemOneSeparateEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.SystemOneRequest
		if err := systemOneBind(c, &req); err != nil {
			return systemOneError(c, systemOneBindStatus(err), systemOneBindMessage(err))
		}
		if req.Model == "" {
			return systemOneError(c, http.StatusBadRequest, "model is required")
		}
		if err := checkSystemOneModel(app, req.Model); err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		if err := checkSystemOneNERModel(app, req.Model); err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		if err := validateSystemOneRequest(&req); err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		parsed, err := parseSystemOneRequest(&req)
		if err != nil {
			return systemOneError(c, http.StatusBadRequest, err.Error())
		}
		classifier, err := resolveClassifier(app, req.Model, parsed.threshold)
		if err != nil {
			return systemOneError(c, http.StatusNotFound, err.Error())
		}
		start := time.Now()
		answers := make(map[string]schema.SystemOneAnswer, len(parsed.questions))
		for i := range parsed.questions {
			entities, err := classifier.TokenClassifyWithLabels(c.Request().Context(), parsed.text, parsed.questions[i].labels)
			if err != nil {
				return systemOneError(c, http.StatusInternalServerError, err.Error())
			}
			answers[parsed.questions[i].id] = buildSystemOneAnswer(&parsed.questions[i], entities)
		}
		latencyMs := float64(time.Since(start).Microseconds()) / 1000.0
		return c.JSON(http.StatusOK, schema.SystemOneResponse{
			Model:     req.Model,
			Answers:   answers,
			Usage:     schema.SystemOneUsage{InputTokens: 0, OutputTokens: 0},
			LatencyMs: r2(latencyMs),
		})
	}
}
