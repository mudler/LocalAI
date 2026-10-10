// SPDX-License-Identifier: MIT
package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These checks catch documentation drift, not the correctness of operator prose.
var _ = Describe("Diagnostics documentation", func() {
	readDoc := func(path string) string {
		data, err := os.ReadFile(filepath.Join("..", "..", "docs", "content", path))
		Expect(err).NotTo(HaveOccurred())
		return string(data)
	}
	It("lists all five exact flags, environments, and defaults in both references", func() {
		defaults := DefaultOptions()
		rows := []struct{ flag, env, value string }{
			{"--pprof", "LOCALAI_PPROF", fmt.Sprint(defaults.Pprof)},
			{"--pprof-address", "LOCALAI_PPROF_ADDRESS", defaults.Address},
			{"--pprof-mutex-profile-fraction", "LOCALAI_PPROF_MUTEX_PROFILE_FRACTION", fmt.Sprint(defaults.MutexProfileFraction)},
			{"--pprof-block-profile-rate", "LOCALAI_PPROF_BLOCK_PROFILE_RATE", fmt.Sprint(defaults.BlockProfileRate)},
			{"--request-phase-timing", "LOCALAI_REQUEST_PHASE_TIMING", fmt.Sprint(defaults.RequestPhaseTiming)},
		}
		for _, path := range []string{"features/diagnostics.md", "reference/cli-reference.md"} {
			doc := readDoc(path)
			for _, row := range rows {
				Expect(doc).To(MatchRegexp(`(?m)^\| `+regexp.QuoteMeta("`"+row.flag+"`")+` \| `+regexp.QuoteMeta("`"+row.value+"`")+` \|[^\n]*`+regexp.QuoteMeta("`$"+row.env+"`")+` \|$`), path)
			}
		}
	})
	It("documents every phase wire constant in the boundary table", func() {
		doc := readDoc("features/diagnostics.md")
		phases := []Phase{PhaseExtraction, PhaseBearerLookup, PhaseDefaultListing, PhaseBodyLookup,
			PhaseConfigLoadDefaults, PhaseAliasResolution, PhaseConfigLockWait, PhaseConfigLockHold,
			PhaseConfigFilter, PhaseFSEnumeration, PhaseLooseFilter, PhaseExistenceFallback, PhaseModelInit,
			PhaseModelRouterCallback, PhaseReloadLockWait, PhaseReloadLockHold, PhaseReloadEnumeration,
			PhaseReloadMetadata, PhaseReloadYAMLRead, PhaseReloadParseDefaults}
		for _, phase := range phases {
			Expect(doc).To(ContainSubstring("| `" + string(phase) + "` |"))
		}
		source, err := os.ReadFile("timing.go")
		Expect(err).NotTo(HaveOccurred())
		// Also catch newly introduced constants omitted from the explicit list.
		wires := regexp.MustCompile(`(?m)^\s*Phase\w+\s+Phase\s*=\s*"([^"]+)"`).FindAllStringSubmatch(string(source), -1)
		Expect(wires).To(HaveLen(len(phases)))
		for _, wire := range wires {
			Expect(doc).To(ContainSubstring("| `" + wire[1] + "` |"))
		}
	})
	It("links both entry points to the feature page", func() {
		readDoc("features/diagnostics.md")
		for _, path := range []string{"reference/cli-reference.md", "getting-started/troubleshooting.md"} {
			Expect(readDoc(path)).To(ContainSubstring(`{{% relref "features/diagnostics" %}}`))
		}
	})
	It("retains sensitive access warnings and direct bounded capture examples", func() {
		doc := readDoc("features/diagnostics.md")
		for _, text := range []string{"`/debug/pprof/cmdline` is unavailable", "same network namespace", "unauthenticated", "sensitive", "umask 077",
			"kubectl -n <namespace> port-forward pod/<selected-frontend-pod> 6060:6060",
			"curl --fail --output cpu.pprof 'http://127.0.0.1:6060/debug/pprof/profile?seconds=10'", "go tool pprof cpu.pprof",
			"/debug/pprof/mutex?seconds=10", "/debug/pprof/block?seconds=10", "/debug/pprof/goroutine?debug=2"} {
			Expect(doc).To(ContainSubstring(text))
		}
		Expect(doc).NotTo(MatchRegexp(`(?i)port-forward\s+(service/|svc/)`))
		Expect(doc).NotTo(MatchRegexp(`(?im)^\s*(kind:\s*(Service|Ingress)|type:\s*NodePort)`))
	})
})
