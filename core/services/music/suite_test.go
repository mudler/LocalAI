// SPDX-License-Identifier: MIT
package music

import (
	"flag"
	"regexp"
	"strings"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var workflowSuite sync.Once

// Go can select several named gates at once. Run their union in one Ginkgo
// invocation, rather than attempting to rerun Ginkgo's process-global suite.
func runWorkflowSuite(t *testing.T) {
	workflowSuite.Do(func() {
		RegisterFailHandler(Fail)
		selection := regexp.MustCompile(strings.SplitN(flag.Lookup("test.run").Value.String(), "/", 2)[0])
		labels := []string{}
		for _, gate := range []struct{ name, label string }{
			{"TestMusicWorkflowTraces", "traces"},
			{"TestMusicWorkflowInvariants", "invariants"},
			{"TestMusicWorkflowCoverage", "coverage"},
			{"TestMusicWorkflowDeterminism", "determinism"},
		} {
			if selection.MatchString(gate.name) {
				labels = append(labels, gate.label)
			}
		}
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
