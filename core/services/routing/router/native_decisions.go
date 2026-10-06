// SPDX-License-Identifier: MIT
package router

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
)

// DecisionsClassifier asks independent native binary questions. Probabilities
// are not normalized across policies: a prompt may activate every label.
type DecisionsClassifier struct {
	runner    backend.DecisionRunner
	labels    []string
	questions map[string]schema.SystemOneQuestion
	threshold float64
}

func NewDecisionsClassifier(policies []ScorePolicy, runner backend.DecisionRunner, threshold float64) (*DecisionsClassifier, error) {
	if runner == nil {
		return nil, fmt.Errorf("decisions runner is required")
	}
	if len(policies) == 0 || len(policies) > systemone.MaxQuestions {
		return nil, fmt.Errorf("decisions requires 1 to %d policies", systemone.MaxQuestions)
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 || threshold > 1 {
		return nil, fmt.Errorf("decisions activation_threshold must be finite and in [0,1]")
	}
	if threshold == 0 {
		threshold = .5
	}
	c := &DecisionsClassifier{runner: runner, threshold: threshold, questions: make(map[string]schema.SystemOneQuestion, len(policies))}
	seen := map[string]bool{}
	for i, p := range policies {
		if strings.TrimSpace(p.Label) == "" || strings.TrimSpace(p.Description) == "" || seen[p.Label] {
			return nil, fmt.Errorf("decisions policies require unique nonblank labels and descriptions")
		}
		seen[p.Label] = true
		criteria, _ := json.Marshal(map[string]string{"false": "The state does not match this policy: " + p.Description, "true": "The state matches this policy: " + p.Description})
		instruction, _ := json.Marshal("Does the state match this policy? " + p.Description)
		c.questions["p"+strconv.Itoa(i)] = schema.SystemOneQuestion{Type: "noul", Instructions: instruction, Criteria: criteria}
		c.labels = append(c.labels, p.Label)
	}
	if err := systemone.ValidateRequest(&schema.SystemOneRequest{State: json.RawMessage(`"x"`), Questions: c.questions}); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *DecisionsClassifier) Name() string { return ClassifierDecisions }
func (c *DecisionsClassifier) Classify(ctx context.Context, p Probe) (Decision, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	release, err := systemone.AcquireAdmission(ctx)
	if err != nil {
		return Decision{}, err
	}
	req, err := p.decisionRequest()
	if err != nil {
		release()
		return Decision{}, err
	}
	req.Questions = c.questions
	err = systemone.ValidateRequest(req)
	release()
	if err != nil {
		return Decision{}, err
	}
	// Native question framing is engine-owned. Raw JSON token counts are not a
	// context budget: do not trim or claim a fit; propagate native rejection.
	response, err := c.runner.Decide(ctx, req)
	if ctx.Err() != nil {
		return Decision{}, ctx.Err()
	}
	if err != nil {
		return Decision{}, err
	}
	if response == nil || len(response.Answers) != len(c.labels) {
		return Decision{}, fmt.Errorf("decisions response must answer exactly the requested questions")
	}
	d := Decision{ActivationThreshold: c.threshold}
	for i, label := range c.labels {
		a, ok := response.Answers["p"+strconv.Itoa(i)]
		if !ok || a.Type != "noul" || a.Noul == nil {
			return Decision{}, fmt.Errorf("decisions response requires numeric noul answers")
		}
		v := *a.Noul
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return Decision{}, fmt.Errorf("decisions probability must be finite and in [0,1]")
		}
		d.LabelScores = append(d.LabelScores, LabelScore{Label: label, Score: v})
		if v >= c.threshold {
			d.Labels = append(d.Labels, label)
		}
		d.Score = math.Max(d.Score, v)
	}
	d.Latency = time.Since(start)
	return d, nil
}
