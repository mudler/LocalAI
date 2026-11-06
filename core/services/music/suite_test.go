// SPDX-License-Identifier: MIT
package music

import (
	"flag"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var workflowSuite sync.Once

// Follow testing.splitRegexp (GOROOT/src/testing/match.go): only unescaped
// slashes and alternatives outside brackets and parentheses separate filters.
// Keep every component so invalid suffixes are errors, not silently ignored.
func workflowRunPatterns(s string) [][]string {
	var alternatives [][]string
	var parts []string
	brackets, parentheses := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			brackets++
		case ']':
			brackets--
			if brackets < 0 {
				brackets = 0
			}
		case '(':
			if brackets == 0 {
				parentheses++
			}
		case ')':
			if brackets == 0 {
				parentheses--
			}
		case '\\':
			i++
		case '/', '|':
			if brackets == 0 && parentheses == 0 {
				separator := s[i]
				parts = append(parts, s[:i])
				s = s[i+1:]
				i = -1
				if separator == '|' {
					alternatives = append(alternatives, parts)
					parts = nil
				}
			}
		}
	}
	return append(alternatives, append(parts, s))
}

func workflowLabels(pattern string) ([]string, error) {
	var selections []*regexp.Regexp
	for _, parts := range workflowRunPatterns(pattern) {
		for i, part := range parts {
			selection, err := regexp.Compile(part)
			if err != nil {
				return nil, fmt.Errorf("invalid workflow test filter %q: %w", part, err)
			}
			if i == 0 {
				selections = append(selections, selection)
			}
		}
	}
	labels := []string{}
	for _, gate := range []struct{ name, label string }{
		{"TestMusicWorkflowTraces", "traces"},
		{"TestMusicWorkflowInvariants", "invariants"},
		{"TestMusicWorkflowCoverage", "coverage"},
		{"TestMusicWorkflowDeterminism", "determinism"},
	} {
		for _, selection := range selections {
			if selection.MatchString(gate.name) {
				labels = append(labels, gate.label)
				break
			}
		}
	}
	return labels, nil
}

// Go can select several named gates at once. Run their union in one Ginkgo
// invocation, rather than attempting to rerun Ginkgo's process-global suite.
func runWorkflowSuite(t *testing.T) {
	workflowSuite.Do(func() {
		RegisterFailHandler(Fail)
		labels, err := workflowLabels(flag.Lookup("test.run").Value.String())
		Expect(err).NotTo(HaveOccurred())
		config, reporter := GinkgoConfiguration()
		filter := "(" + strings.Join(labels, " || ") + ")"
		if config.LabelFilter != "" {
			filter += " && (" + config.LabelFilter + ")"
		}
		config.LabelFilter = filter
		config.FailOnEmpty = true
		RunSpecs(t, "Music workflow", config, reporter)
	})
}

func TestMusicWorkflowTraces(t *testing.T)      { runWorkflowSuite(t) }
func TestMusicWorkflowInvariants(t *testing.T)  { runWorkflowSuite(t) }
func TestMusicWorkflowCoverage(t *testing.T)    { runWorkflowSuite(t) }
func TestMusicWorkflowDeterminism(t *testing.T) { runWorkflowSuite(t) }
