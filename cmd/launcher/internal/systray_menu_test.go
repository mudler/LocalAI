// SPDX-License-Identifier: MIT
package launcher

import (
	"fyne.io/fyne/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tray actions by mode", func() {
	labels := func(items []*fyne.MenuItem) []string {
		var out []string
		for _, i := range items {
			if !i.IsSeparator {
				out = append(out, i.Label)
			}
		}
		return out
	}
	It("names the worker actions and opens the deployment UI, not a local server UI", func() {
		got := labels(roleMenuItems(roleWorker, false, true, func() {}, func() {}))
		Expect(got).To(ConsistOf("Start worker", "Open deployment UI"))
		got = labels(roleMenuItems(roleWorker, true, true, func() {}, func() {}))
		Expect(got).To(ConsistOf("Stop worker", "Open deployment UI"))
	})
	It("hides the deployment UI action when no valid frontend URL is saved", func() {
		Expect(labels(roleMenuItems(roleWorker, false, false, func() {}, func() {}))).To(ConsistOf("Start worker"))
	})
	It("keeps the standalone actions unchanged", func() {
		Expect(labels(roleMenuItems(roleStandalone, false, true, func() {}, func() {}))).To(ConsistOf("▶️ Start LocalAI"))
		Expect(labels(roleMenuItems(roleStandalone, true, true, func() {}, func() {}))).To(ConsistOf("🛑 Stop LocalAI", "Open WebUI"))
	})
})
