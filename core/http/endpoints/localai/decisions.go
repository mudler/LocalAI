// SPDX-License-Identifier: MIT
package localai

import (
	"encoding/json"
	"fmt"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/middleware"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
)

// All identifiers are adapter-owned; public names never enter the internal namespace.
func decisionQuestionID(i int) string { return fmt.Sprintf("q%06d", i) }
func decisionChoiceID(i int) string   { return fmt.Sprintf("c%06d", i) }

func convertDecisionsRequest(req *schema.DecisionsRequest) (*schema.SystemOneRequest, error) {
	out := &schema.SystemOneRequest{Model: req.Model, Questions: map[string]schema.SystemOneQuestion{}}
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	var texts, images []string
	var text string
	if len(req.Input) > 0 && req.Input[0] == '"' {
		if err := json.Unmarshal(req.Input, &text); err != nil {
			return nil, err
		}
		texts = append(texts, text)
	} else {
		var messages []schema.DecisionInputMessage
		if err := json.Unmarshal(req.Input, &messages); err != nil || len(messages) == 0 {
			return nil, fmt.Errorf("input must be text or user messages")
		}
		for _, message := range messages {
			if message.Role != "user" || (message.Type != "" && message.Type != "message") {
				return nil, fmt.Errorf("input supports only user messages")
			}
			if len(message.Content) > 0 && message.Content[0] == '"' {
				if err := json.Unmarshal(message.Content, &text); err != nil {
					return nil, err
				}
				texts = append(texts, text)
				continue
			}
			var parts []schema.DecisionInputPart
			if err := json.Unmarshal(message.Content, &parts); err != nil || len(parts) == 0 {
				return nil, fmt.Errorf("message content must be text or input parts")
			}
			for _, part := range parts {
				switch part.Type {
				case "input_text":
					if part.Text == nil {
						return nil, fmt.Errorf("input_text requires text")
					}
					texts = append(texts, *part.Text)
				case "input_image":
					if part.Detail != nil && *part.Detail != "auto" {
						return nil, fmt.Errorf("only auto image detail is supported")
					}
					images = append(images, part.ImageURL)
				default:
					return nil, fmt.Errorf("unsupported input part %q", part.Type)
				}
			}
		}
	}
	out.State, _ = json.Marshal(strings.Join(texts, "\n"))
	if len(images) > 0 {
		out.Images, _ = json.Marshal(images)
		// Image-only evidence must not become an invalid blank text state.
		if strings.TrimSpace(strings.Join(texts, "\n")) == "" {
			out.State = json.RawMessage(`{}`)
		}
	}
	names := map[string]bool{}
	for i, q := range req.Questions {
		if q.Name != nil {
			if names[*q.Name] {
				return nil, fmt.Errorf("duplicate question name %q", *q.Name)
			}
			names[*q.Name] = true
		}
		if q.Instructions == nil {
			return nil, fmt.Errorf("question instructions are required")
		}
		internal := schema.SystemOneQuestion{Type: q.Type}
		internal.Instructions, _ = json.Marshal(*q.Instructions)
		switch q.Type {
		case "predicate":
			if q.Choices != nil || q.Levels != nil {
				return nil, fmt.Errorf("predicate does not support choices or levels")
			}
			internal.Type = "noul"
		case "choice":
			if q.Levels != nil {
				return nil, fmt.Errorf("choice does not support levels")
			}
			criteria := map[string]string{}
			values := map[string]bool{}
			for j, choice := range q.Choices {
				var v any
				if err := json.Unmarshal(choice.Value, &v); err != nil {
					return nil, fmt.Errorf("choice value must be string or boolean")
				}
				switch v.(type) {
				case string, bool:
				default:
					return nil, fmt.Errorf("choice value must be string or boolean")
				}
				canonical, _ := json.Marshal(v)
				if values[string(canonical)] {
					return nil, fmt.Errorf("duplicate choice value")
				}
				values[string(canonical)] = true
				label := string(canonical)
				if choice.Description != "" {
					label += ": " + choice.Description
				}
				criteria[decisionChoiceID(j)] = label
			}
			internal.Criteria, _ = json.Marshal(criteria)
		case "score":
			if q.Choices != nil {
				return nil, fmt.Errorf("score does not support choices")
			}
			levels := []string{}
			for _, level := range q.Levels {
				if level.Label == nil {
					return nil, fmt.Errorf("score level requires label")
				}
				label := *level.Label
				if level.Description != "" {
					label += ": " + level.Description
				}
				levels = append(levels, label)
			}
			internal.Criteria, _ = json.Marshal(levels)
		default:
			return nil, fmt.Errorf("unsupported question type %q", q.Type)
		}
		out.Questions[decisionQuestionID(i)] = internal
	}
	if err := systemone.ValidateRequestStructure(out); err != nil {
		return nil, err
	}
	return out, nil
}

func decisionUnit(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 1
}

func convertDecisionsResponse(req *schema.DecisionsRequest, raw []byte) (*schema.DecisionsResponse, error) {
	var source struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			schema.SystemOneAnswer
			// Pointers distinguish an explicit JSON null from a valid zero.
			Probabilities map[string]*float64 `json:"probabilities"`
		} `json:"answers"`
		Usage *struct {
			Input         *int           `json:"input_tokens"`
			Output        *int           `json:"output_tokens"`
			InputDetails  map[string]int `json:"input_tokens_details"`
			OutputDetails map[string]int `json:"output_tokens_details"`
		} `json:"usage"`
	}
	invalid := fmt.Errorf("invalid decision backend response")
	if len(raw) > systemone.MaxResponseBytes {
		return nil, invalid
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, invalid
	}
	if source.Model == "" || len(source.Answers) != len(req.Questions) || source.Usage == nil || source.Usage.Input == nil || source.Usage.Output == nil {
		return nil, invalid
	}
	u := source.Usage
	if *u.Input < 0 || *u.Output < 0 || *u.Input > int(^uint(0)>>1)-*u.Output {
		return nil, invalid
	}
	if u.InputDetails == nil {
		u.InputDetails = map[string]int{}
	}
	if u.OutputDetails == nil {
		u.OutputDetails = map[string]int{}
	}
	for _, details := range []map[string]int{u.InputDetails, u.OutputDetails} {
		for _, n := range details {
			if n < 0 {
				return nil, invalid
			}
		}
	}
	out := &schema.DecisionsResponse{Model: source.Model, Answers: make([]schema.DecisionAnswer, 0, len(req.Questions)), Usage: schema.DecisionUsage{InputTokens: *u.Input, OutputTokens: *u.Output, TotalTokens: *u.Input + *u.Output, InputTokensDetails: u.InputDetails, OutputTokensDetails: u.OutputDetails}}
	for i, q := range req.Questions {
		a, ok := source.Answers[decisionQuestionID(i)]
		if !ok {
			return nil, invalid
		}
		answer := schema.DecisionAnswer{Type: q.Type, Name: q.Name}
		if a.Type == "refusal" {
			answer.Type = "refusal"
			out.Answers = append(out.Answers, answer)
			continue
		}
		switch q.Type {
		case "predicate":
			if a.Type != "noul" || !decisionUnit(a.Noul) {
				return nil, invalid
			}
			answer.Probability = a.Noul
		case "choice", "score":
			if a.Type != q.Type || !decisionUnit(a.Confidence) {
				return nil, invalid
			}
			answer.Confidence = a.Confidence
			count := len(q.Choices)
			if q.Type == "score" {
				count = len(q.Levels)
			}
			if len(a.Probabilities) != count {
				return nil, invalid
			}
			total := 0.0
			chosen := false
			for j := 0; j < count; j++ {
				key := decisionChoiceID(j)
				if q.Type == "score" {
					key = strconv.Itoa(j)
				}
				p, ok := a.Probabilities[key]
				if !ok || !decisionUnit(p) {
					return nil, invalid
				}
				total += *p
				probability := schema.DecisionProbability{Probability: *p}
				if q.Type == "choice" {
					probability.Value = q.Choices[j].Value
					if a.Choice != nil && *a.Choice == key {
						answer.Choice = q.Choices[j].Value
						chosen = true
					}
				} else {
					probability.Value = json.RawMessage(strconv.Itoa(j))
					probability.Label = q.Levels[j].Label
				}
				answer.Probabilities = append(answer.Probabilities, probability)
			}
			// SystemOne rounds probabilities to two decimals independently.
			if math.Abs(total-1) > 0.005*float64(count)+1e-9 {
				return nil, invalid
			}
			if q.Type == "choice" && !chosen {
				return nil, invalid
			}
			if q.Type == "score" {
				if a.Score == nil || math.IsNaN(*a.Score) || math.IsInf(*a.Score, 0) || *a.Score < 0 || *a.Score > float64(count-1) {
					return nil, invalid
				}
				// Keep the score consistent with the rounded distribution we emit.
				score := 0.0
				for j, probability := range answer.Probabilities {
					score += float64(j) * probability.Probability
				}
				answer.Score = &score
			}
		default:
			return nil, invalid
		}
		out.Answers = append(out.Answers, answer)
	}
	return out, nil
}

// DecisionsEndpoint handles the OpenAI Decisions wire contract.
// @Summary Answer ordered predicate, choice and score questions.
// @Description Uses the same local decision and NER execution as SystemOne. Supports inline images on capable backends; confidence is not OpenAI calibrated.
// @Tags systemone
// @Accept json
// @Produce json
// @Param request body schema.DecisionsRequest true "input + questions + model"
// @Success 200 {object} schema.DecisionsResponse
// @Router /v1/decisions [post]
func DecisionsEndpoint(app *application.Application) echo.HandlerFunc {
	return decisionsEndpoint(func(c echo.Context, req *schema.SystemOneRequest, respond func(string) error) error {
		return executeSystemOne(c, app, req, respond)
	})
}

// The seam keeps handler tests independent of model downloads, not of HTTP admission.
func decisionsEndpoint(execute func(echo.Context, *schema.SystemOneRequest, func(string) error) error) echo.HandlerFunc {
	return func(c echo.Context) error {
		release, err := systemone.AcquireAdmission(c.Request().Context())
		if err != nil {
			return systemOneError(c, http.StatusServiceUnavailable, err.Error())
		}
		defer release()
		raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, systemone.MaxImageBodyBytes))
		if err != nil {
			return systemOneError(c, systemOneBindStatus(err), systemOneBindMessage(err))
		}
		var req schema.DecisionsRequest
		err = json.Unmarshal(raw, &req)
		var internal *schema.SystemOneRequest
		if err == nil {
			internal, err = convertDecisionsRequest(&req)
		}
		limit := systemone.MaxBodyBytes
		if err == nil {
			limit, err = systemone.RequestBodyLimit(internal)
		}
		if len(raw) > limit {
			return systemOneError(c, http.StatusRequestEntityTooLarge, "decision request body exceeds limit")
		}
		if err != nil {
			return systemOneError(c, systemOneInputStatus(err), err.Error())
		}
		return execute(c, internal, func(raw string) error {
			response, err := convertDecisionsResponse(&req, []byte(raw))
			if err != nil {
				return systemOneError(c, http.StatusInternalServerError, err.Error())
			}
			middleware.StampUsage(c, response.Model, response.Usage.InputTokens, response.Usage.OutputTokens)
			return c.JSON(http.StatusOK, response)
		})
	}
}
