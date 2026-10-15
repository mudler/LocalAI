// SPDX-License-Identifier: MIT
// Package systemone shares decision request validation across internal and HTTP callers.
package systemone

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
)

const (
	MaxBodyBytes = 64 << 10
	MaxQuestions = 64
)

type ErrorKind string

const (
	InvalidRequest     ErrorKind = "invalid_request"
	UnsupportedBackend ErrorKind = "unsupported_backend"
)

// ValidationError separates invalid input from unsupported backend capabilities.
// HTTP callers may map these kinds to 400 and 501 respectively.
type ValidationError struct {
	Kind ErrorKind
	Err  error
}

func (e *ValidationError) Error() string { return e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// ValidateRequestStructure checks request semantics without re-encoding its
// fields. HTTP callers bound the original wire bytes before binding; escaping
// during serialization must not impose a second, different HTTP size limit.
func ValidateRequestStructure(req *schema.SystemOneRequest) error {
	if req == nil {
		return &ValidationError{InvalidRequest, fmt.Errorf("request is required")}
	}
	if err := validateRequest(req); err != nil {
		return &ValidationError{InvalidRequest, err}
	}
	images, err := CollectImages(req)
	if err != nil {
		return err
	}
	return ValidateImages(images)
}

// ValidateRequest additionally bounds the serialized internal transport body.
// Internal callers have no HTTP reader on which to enforce the wire limit.
func ValidateRequest(req *schema.SystemOneRequest) error {
	if err := ValidateRequestStructure(req); err != nil {
		return err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return &ValidationError{InvalidRequest, err}
	}
	limit, err := RequestBodyLimit(req)
	if err != nil {
		return err
	}
	if len(body) > limit {
		return &ValidationError{InvalidRequest, fmt.Errorf("request body exceeds %d KiB", limit>>10)}
	}
	return nil
}

// ValidateDecisionModel is stricter than legacy HTTP admission: the router
// never guesses a model's usecase and never falls back to NER or generation.
func ValidateDecisionModel(cfg config.ModelConfig) error {
	if cfg.KnownUsecases == nil || *cfg.KnownUsecases&config.FLAG_DECISIONS == 0 {
		return &ValidationError{InvalidRequest, fmt.Errorf("model %q must explicitly declare known_usecases: [decisions]", cfg.Name)}
	}
	if !BackendSupportsScore(cfg.Backend) {
		return &ValidationError{UnsupportedBackend, fmt.Errorf("backend %q does not support decisions", cfg.Backend)}
	}
	return nil
}
func validateRequest(req *schema.SystemOneRequest) error {
	if len(req.State) == 0 || string(req.State) == "null" {
		return fmt.Errorf("state is required")
	}
	var state any
	if err := json.Unmarshal(req.State, &state); err != nil {
		return fmt.Errorf("state is not valid JSON: %w", err)
	}
	if state == nil {
		return fmt.Errorf("state is required")
	}
	if s, ok := state.(string); ok && strings.TrimSpace(s) == "" {
		return fmt.Errorf("state is required")
	}
	if len(req.Questions) == 0 {
		return fmt.Errorf("questions is required and must contain at least one question")
	}
	if len(req.Questions) > MaxQuestions {
		return fmt.Errorf("questions must contain at most %d questions", MaxQuestions)
	}
	qids := make([]string, 0, len(req.Questions))
	for id := range req.Questions {
		qids = append(qids, id)
	}
	sort.Strings(qids)
	for _, id := range qids {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("question ids must not be blank")
		}
		q := req.Questions[id]
		switch q.Type {
		case "choice":
			var criteria map[string]json.RawMessage
			if err := json.Unmarshal(q.Criteria, &criteria); err != nil {
				return fmt.Errorf("question %q (choice) requires a criteria object", id)
			}
			if len(criteria) < 2 {
				return fmt.Errorf("question %q (choice) requires at least 2 options", id)
			}
			for k := range criteria {
				if strings.TrimSpace(k) == "" {
					return fmt.Errorf("question %q (choice) has a blank option key", id)
				}
			}
		case "score":
			var criteria []json.RawMessage
			if err := json.Unmarshal(q.Criteria, &criteria); err != nil {
				return fmt.Errorf("question %q (score) requires a criteria array", id)
			}
			if len(criteria) < 2 {
				return fmt.Errorf("question %q (score) requires at least 2 levels", id)
			}
		case "noul":
			if len(q.Criteria) == 0 || string(q.Criteria) == "null" {
				continue
			}
			var criteria map[string]json.RawMessage
			if err := json.Unmarshal(q.Criteria, &criteria); err != nil {
				return fmt.Errorf("question %q (noul) criteria must be an object with \"false\" and \"true\" descriptions", id)
			}
			for k := range criteria {
				if k != "false" && k != "true" {
					return fmt.Errorf("question %q (noul) criteria may only have \"false\" and \"true\" keys", id)
				}
			}
		default:
			return fmt.Errorf("question %q has unknown type: %s", id, q.Type)
		}
	}
	return nil
}

func ModelAllowed(cfg config.ModelConfig) error {
	if cfg.KnownUsecases == nil {
		return nil
	}
	if *cfg.KnownUsecases&(config.FLAG_DECISIONS|config.FLAG_TOKEN_CLASSIFY) != 0 {
		return nil
	}
	return fmt.Errorf("model %q does not declare the decisions usecase (known_usecases: [decisions])", cfg.Name)
}

func NERAllowed(cfg config.ModelConfig) error {
	if cfg.KnownUsecases == nil {
		return nil
	}
	declared := *cfg.KnownUsecases
	if declared&config.FLAG_DECISIONS != 0 && declared&config.FLAG_TOKEN_CLASSIFY == 0 {
		return fmt.Errorf("model %q is a decision model: /permute and /separate use the NER path, use POST /v1/systemone instead", cfg.Name)
	}
	return nil
}

func UsesDecisionPipeline(cfg config.ModelConfig) bool {
	if !BackendSupportsScore(cfg.Backend) {
		return false
	}
	if cfg.KnownUsecases == nil {
		return true
	}
	declared := *cfg.KnownUsecases
	if declared&config.FLAG_DECISIONS != 0 {
		return true
	}
	return declared&config.FLAG_TOKEN_CLASSIFY == 0
}

func BackendSupportsScore(backendName string) bool {
	cap := config.GetBackendCapability(backendName)
	if cap == nil {
		return false
	}
	return slices.Contains(cap.GRPCMethods, config.MethodScore)
}
